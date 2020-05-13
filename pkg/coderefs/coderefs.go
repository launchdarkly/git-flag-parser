package coderefs

import (
	"container/list"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/launchdarkly/ld-find-code-refs/internal/command"
	"github.com/launchdarkly/ld-find-code-refs/internal/helpers"
	"github.com/launchdarkly/ld-find-code-refs/internal/ld"
	"github.com/launchdarkly/ld-find-code-refs/internal/log"
	"github.com/launchdarkly/ld-find-code-refs/internal/options"
	"github.com/launchdarkly/ld-find-code-refs/internal/validation"
	"github.com/launchdarkly/ld-find-code-refs/internal/version"
)

// These are defensive limits intended to prevent corner cases stemming from
// large repos, false positives, etc. The goal is a) to prevent the program
// from taking a very long time to run and b) to prevent the program from
// PUTing a massive json payload. These limits will likely be tweaked over
// time. The LaunchDarkly backend will also apply limits.
const (
	minFlagKeyLen                     = 3     // Minimum flag key length helps reduce the number of false positives
	maxFileCount                      = 10000 // Maximum number of files containing code references
	maxLineCharCount                  = 500   // Maximum number of characters per line
	maxHunkCount                      = 25000 // Maximum number of total code references
	maxHunkedLinesPerFileAndFlagCount = 500   // Maximum number of lines per flag in a file
	maxProjKeyLength                  = 20    // Maximum project key length
)

// map of flag keys to slices of lines those flags occur on
type flagReferenceMap map[string][]*list.Element

// this struct contains a linked list of all the search result lines
// for a single file, and a map of flag keys to slices of lines where
// those flags occur.
type fileSearchResults struct {
	path                  string
	fileSearchResultLines *list.List
	flagReferenceMap
}

type branch struct {
	Name             string
	Head             string
	UpdateSequenceId *int
	SyncTime         int64
	SearchResults    searchResultLines
}

func Scan(opts options.Options) {
	// Don't log ld access token
	optionsForLog := opts
	optionsForLog.AccessToken = helpers.MaskAccessToken(optionsForLog.AccessToken)
	log.Debug.Printf("starting ld-find-code-refs with options:\n %+v\n", optionsForLog)

	dir := opts.Dir
	absPath, err := validation.NormalizeAndValidatePath(dir)
	if err != nil {
		log.Error.Fatalf("could not validate directory option: %s", err)
	}

	log.Info.Printf("absolute directory path: %s", absPath)
	searchClient, err := command.NewAgClient(absPath)
	if err != nil {
		log.Error.Fatalf("%s", err)
	}

	gitClient, err := command.NewGitClient(absPath, opts.Branch)
	if err != nil {
		log.Error.Fatalf("%s", err)
	}

	projKey := opts.ProjKey

	// Check for potential sdk keys or access tokens provided as the project key
	if len(projKey) > maxProjKeyLength {
		if strings.HasPrefix(projKey, "sdk-") {
			log.Warning.Printf("provided projKey (%s) appears to be a LaunchDarkly SDK key", "sdk-xxxx")
		} else if strings.HasPrefix(projKey, "api-") {
			log.Warning.Printf("provided projKey (%s) appears to be a LaunchDarkly API access token", "api-xxxx")
		}
	}

	ldApi := ld.InitApiClient(ld.ApiOptions{ApiKey: opts.AccessToken, BaseUri: opts.BaseUri, ProjKey: projKey, UserAgent: "LDFindCodeRefs/" + version.Version})
	repoParams := ld.RepoParams{
		Type:              opts.RepoType,
		Name:              opts.RepoName,
		Url:               opts.RepoUrl,
		CommitUrlTemplate: opts.CommitUrlTemplate,
		HunkUrlTemplate:   opts.HunkUrlTemplate,
		DefaultBranch:     opts.DefaultBranch,
	}

	isDryRun := opts.DryRun

	ignoreServiceErrors := opts.IgnoreServiceErrors
	if !isDryRun {
		err = ldApi.MaybeUpsertCodeReferenceRepository(repoParams)
		if err != nil {
			fatalServiceError(err, ignoreServiceErrors)
		}
	}

	flags, err := getFlags(ldApi)
	if err != nil {
		fatalServiceError(fmt.Errorf("could not retrieve flag keys from LaunchDarkly: %w", err), ignoreServiceErrors)
	}

	if len(flags) == 0 {
		log.Info.Printf("no flag keys found for project: %s, exiting early", projKey)
		os.Exit(0)
	}

	filteredFlags, omittedFlags := filterShortFlagKeys(flags)
	if len(filteredFlags) == 0 {
		log.Info.Printf("no flag keys longer than the minimum flag key length (%v) were found for project: %s, exiting early",
			minFlagKeyLen, projKey)
		os.Exit(0)
	} else if len(omittedFlags) > 0 {
		log.Warning.Printf("omitting %d flags with keys less than minimum (%d)", len(omittedFlags), minFlagKeyLen)
	}

	aliases, err := generateAliases(filteredFlags, opts.Aliases, dir)
	if err != nil {
		log.Error.Fatalf("failed to create flag key aliases: %v", err)
	}

	ctxLines := opts.ContextLines
	var updateId *int
	if opts.UpdateSequenceId >= 0 {
		updateIdOption := opts.UpdateSequenceId
		updateId = &updateIdOption
	}
	b := &branch{
		Name:             gitClient.GitBranch,
		UpdateSequenceId: updateId,
		SyncTime:         makeTimestamp(),
		Head:             gitClient.GitSha,
	}

	// Configure delimiters
	delims := []string{`"`, `'`, "`"}
	if opts.Delimiters.DisableDefaults {
		delims = []string{}
	}
	delims = append(delims, opts.Delimiters.Additional...)
	delimString := strings.Join(helpers.Dedupe(delims), "")

	refs, err := findReferences(searchClient, filteredFlags, aliases, ctxLines, delimString)
	if err != nil {
		log.Error.Fatalf("error searching for flag key references: %s", err)
	}

	b.SearchResults = refs
	sort.Sort(b.SearchResults)

	branchRep := b.makeBranchRep(projKey, ctxLines)

	outDir := opts.OutDir
	if outDir != "" {
		outPath, err := branchRep.WriteToCSV(outDir, projKey, repoParams.Name, gitClient.GitSha)
		if err != nil {
			log.Error.Fatalf("error writing code references to csv: %s", err)
		}
		log.Info.Printf("wrote code references to %s", outPath)
	}

	if opts.Debug {
		branchRep.PrintReferenceCountTable()
	}

	if isDryRun {
		log.Info.Printf(
			"dry run found %d code references across %d flags and %d files",
			branchRep.TotalHunkCount(),
			len(filteredFlags),
			len(branchRep.References),
		)
		return
	}

	log.Info.Printf(
		"sending %d code references across %d flags and %d files to LaunchDarkly for project: %s",
		branchRep.TotalHunkCount(),
		len(filteredFlags),
		len(branchRep.References),
		projKey,
	)

	err = ldApi.PutCodeReferenceBranch(branchRep, repoParams.Name)
	switch {
	case err == ld.BranchUpdateSequenceIdConflictErr:
		if b.UpdateSequenceId != nil {
			log.Warning.Printf("updateSequenceId (%d) must be greater than previously submitted updateSequenceId", *b.UpdateSequenceId)
		}
	case err == ld.EntityTooLargeErr:
		log.Error.Fatalf("code reference payload too large for LaunchDarkly API - consider excluding more files with .ldignore")
	case err != nil:
		fatalServiceError(fmt.Errorf("error sending code references to LaunchDarkly: %w", err), ignoreServiceErrors)
	}

	log.Info.Printf("attempting to prune old code reference data from LaunchDarkly")
	remoteBranches, err := gitClient.RemoteBranches()
	if err != nil {
		log.Warning.Printf("unable to retrieve branch list from remote, skipping code reference pruning: %s", err)
	} else {
		err = deleteStaleBranches(ldApi, repoParams.Name, remoteBranches)
		if err != nil {
			fatalServiceError(fmt.Errorf("failed to mark old branches for code reference pruning: %w", err), ignoreServiceErrors)
		}
	}
}

func deleteStaleBranches(ldApi ld.ApiClient, repoName string, remoteBranches map[string]bool) error {
	branches, err := ldApi.GetCodeReferenceRepositoryBranches(repoName)
	if err != nil {
		return err
	}

	staleBranches := calculateStaleBranches(branches, remoteBranches)
	if len(staleBranches) > 0 {
		log.Debug.Printf("marking stale branches for code reference pruning: %v", staleBranches)
		err = ldApi.PostDeleteBranchesTask(repoName, staleBranches)
		if err != nil {
			return err
		}
	}

	return nil
}

func calculateStaleBranches(branches []ld.BranchRep, remoteBranches map[string]bool) []string {
	staleBranches := []string{}
	for _, branch := range branches {
		if !remoteBranches[branch.Name] {
			staleBranches = append(staleBranches, branch.Name)
		}
	}
	log.Info.Printf("found %d stale branches to be marked for code reference pruning", len(staleBranches))
	return staleBranches
}

// Very short flag keys lead to many false positives when searching in code,
// so we filter them out.
func filterShortFlagKeys(flags []string) (filtered []string, omitted []string) {
	filteredFlags := []string{}
	omittedFlags := []string{}
	for _, flag := range flags {
		if len(flag) >= minFlagKeyLen {
			filteredFlags = append(filteredFlags, flag)
		} else {
			omittedFlags = append(omittedFlags, flag)
		}
	}
	return filteredFlags, omittedFlags
}

func getFlags(ldApi ld.ApiClient) ([]string, error) {
	flags, err := ldApi.GetFlagKeyList()
	if err != nil {
		return nil, err
	}
	return flags, nil
}

func findReferencedFlags(ref string, aliases map[string][]string, delims string) map[string][]string {
	ret := make(map[string][]string, len(aliases))
	for key, flagAliases := range aliases {
		matcher := regexp.MustCompile(regexp.QuoteMeta(key))
		if len(delims) > 0 {
			matcher = regexp.MustCompile(fmt.Sprintf("[%s]%s[%s]", delims, regexp.QuoteMeta(key), delims))
		}
		if matcher.MatchString(ref) {
			ret[key] = make([]string, 0, len(flagAliases))
		}
		for _, alias := range flagAliases {
			aliasMatcher := regexp.MustCompile(regexp.QuoteMeta(alias))
			if aliasMatcher.MatchString(ref) {
				if ret[key] == nil {
					ret[key] = make([]string, 0, len(flagAliases))
				}
				ret[key] = append(ret[key], alias)
			}
		}
	}
	return ret
}

func (b *branch) makeBranchRep(projKey string, ctxLines int) ld.BranchRep {
	return ld.BranchRep{
		Name:             strings.TrimPrefix(b.Name, "refs/heads/"),
		Head:             b.Head,
		UpdateSequenceId: b.UpdateSequenceId,
		SyncTime:         b.SyncTime,
		References:       b.SearchResults.makeReferenceHunksReps(projKey, ctxLines),
	}
}

func (g searchResultLines) makeReferenceHunksReps(projKey string, ctxLines int) []ld.ReferenceHunksRep {
	reps := []ld.ReferenceHunksRep{}

	aggregatedSearchResults := g.aggregateByPath()

	if len(aggregatedSearchResults) > maxFileCount {
		log.Warning.Printf("found %d files with code references, which exceeded the limit of %d", len(aggregatedSearchResults), maxFileCount)
		aggregatedSearchResults = aggregatedSearchResults[0:maxFileCount]
	}

	numHunks := 0

	shouldSuppressUnexpectedError := false
	for _, fileSearchResults := range aggregatedSearchResults {
		if numHunks > maxHunkCount {
			log.Warning.Printf("found %d code references across all files, which exceeeded the limit of %d. halting code reference search", numHunks, maxHunkCount)
			break
		}

		hunks := fileSearchResults.makeHunkReps(projKey, ctxLines)

		if len(hunks) == 0 && !shouldSuppressUnexpectedError {
			log.Error.Printf("expected code references but found none in '%s'", fileSearchResults.path)
			log.Debug.Printf("%+v", fileSearchResults)
			// if this error occurred, it's likely to occur for many other files, and create a lot of noise. So, suppress the message for all other occurrences
			shouldSuppressUnexpectedError = true
			continue
		}

		numHunks += len(hunks)

		reps = append(reps, ld.ReferenceHunksRep{Path: fileSearchResults.path, Hunks: hunks})
	}
	return reps
}

// Assumes invariant: searchResultLines will already be sorted by path.
func (g searchResultLines) aggregateByPath() []fileSearchResults {
	allFileResults := []fileSearchResults{}

	if len(g) == 0 {
		return allFileResults
	}

	// initialize first file
	currentFileResults := fileSearchResults{
		path:                  g[0].Path,
		flagReferenceMap:      flagReferenceMap{},
		fileSearchResultLines: list.New(),
	}

	for _, searchResult := range g {
		// If we reach a search result with a new path, append the old one to our list and start a new one
		if searchResult.Path != currentFileResults.path {
			allFileResults = append(allFileResults, currentFileResults)

			currentFileResults = fileSearchResults{
				path:                  searchResult.Path,
				flagReferenceMap:      flagReferenceMap{},
				fileSearchResultLines: list.New(),
			}
		}

		elem := currentFileResults.addSearchResult(searchResult)

		if len(searchResult.FlagKeys) > 0 {
			for flagKey := range searchResult.FlagKeys {
				currentFileResults.addFlagReference(flagKey, elem)
			}
		}
	}

	// append last file
	allFileResults = append(allFileResults, currentFileResults)

	return allFileResults
}

func (fsr *fileSearchResults) addSearchResult(searchResult searchResultLine) *list.Element {
	prev := fsr.fileSearchResultLines.Back()
	if prev != nil && prev.Value.(searchResultLine).LineNum > searchResult.LineNum {
		// This should never happen, as `ag` (and any other search program we might use
		// should always return search results sorted by line number. We sanity check
		// that lines are sorted _just in case_ since the downstream hunking algorithm
		// only works on sorted lines.
		log.Error.Fatalf("search results returned out of order")
	}

	return fsr.fileSearchResultLines.PushBack(searchResult)
}

func (fsr *fileSearchResults) addFlagReference(key string, ref *list.Element) {
	_, ok := fsr.flagReferenceMap[key]

	if ok {
		fsr.flagReferenceMap[key] = append(fsr.flagReferenceMap[key], ref)
	} else {
		fsr.flagReferenceMap[key] = []*list.Element{ref}
	}
}

func (fsr fileSearchResults) makeHunkReps(projKey string, ctxLines int) []ld.HunkRep {
	hunks := []ld.HunkRep{}

	for flagKey, flagReferences := range fsr.flagReferenceMap {
		flagHunks := buildHunksForFlag(projKey, flagKey, fsr.path, flagReferences, ctxLines)
		hunks = append(hunks, flagHunks...)
	}

	return hunks
}

func buildHunksForFlag(projKey, flag, path string, flagReferences []*list.Element, ctxLines int) []ld.HunkRep {
	hunks := []ld.HunkRep{}

	var previousHunk *ld.HunkRep
	var currentHunk ld.HunkRep

	lastSeenLineNum := -1

	var hunkStringBuilder strings.Builder

	appendToPreviousHunk := false

	numHunkedLines := 0

	for _, ref := range flagReferences {
		// Each ref is either the start of a new hunk or a continuation of the previous hunk.
		// NOTE: its possible that this flag reference is totally contained in the previous hunk
		ptr := ref
		numCtxLinesBeforeFlagRef := 0

		// Attempt to seek to the start of the new hunk.
		for i := 0; i < ctxLines; i++ {
			// If we seek to a nil pointer, we're at the start of the file and can go no further.
			if ptr.Prev() != nil {
				ptr = ptr.Prev()
				numCtxLinesBeforeFlagRef++
			}

			// If we seek earlier than the end of the last hunk, this reference overlaps at least
			// partially with the last hunk and we should (possibly) expand the previous hunk rather than
			// starting a new hunk.
			if ptr.Value.(searchResultLine).LineNum <= lastSeenLineNum {
				appendToPreviousHunk = true
			}
		}

		// If we are starting a new hunk, initialize it
		if !appendToPreviousHunk {
			currentHunk = initHunk(projKey, flag)
			currentHunk.StartingLineNumber = ptr.Value.(searchResultLine).LineNum
			hunkStringBuilder.Reset()
		}

		// From the current position (at the theoretical start of the hunk) seek forward line by line X times,
		// where X = (numCtxLinesBeforeFlagRef + 1 + ctxLines). Note that if the flag reference occurs close to the
		// start of the file, numCtxLines may be smaller than ctxLines.
		//   For each line, check if we have seeked past the end of the last hunk
		//     If so: write that line to the hunkStringBuilder
		//     Record that line as the last seen line.
		for i := 0; i < numCtxLinesBeforeFlagRef+1+ctxLines; i++ {
			ptrLineNum := ptr.Value.(searchResultLine).LineNum
			if ptrLineNum > lastSeenLineNum {
				lineText := truncateLine(ptr.Value.(searchResultLine).LineText)
				hunkStringBuilder.WriteString(lineText + "\n")
				lastSeenLineNum = ptrLineNum
				numHunkedLines += 1
				currentHunk.Aliases = append(currentHunk.Aliases, ptr.Value.(searchResultLine).FlagKeys[flag]...)
			}

			if ptr.Next() != nil {
				ptr = ptr.Next()
			}
		}

		if appendToPreviousHunk {
			previousHunk.Lines = hunkStringBuilder.String()
			appendToPreviousHunk = false
		} else {
			currentHunk.Lines = hunkStringBuilder.String()
			currentHunk.Aliases = helpers.Dedupe(currentHunk.Aliases)
			hunks = append(hunks, currentHunk)
			previousHunk = &hunks[len(hunks)-1]
		}

		// If we have written more than the max. allowed number of lines for this file and flag, finish this hunk and exit early.
		// This guards against a situation where the user has very long files with many false positive matches.
		if numHunkedLines > maxHunkedLinesPerFileAndFlagCount {
			log.Warning.Printf("found %d code reference lines in %s for the flag %s, which exceeded the limit of %d. truncating code references for this path and flag.",
				numHunkedLines, path, flag, maxHunkedLinesPerFileAndFlagCount)
			return hunks
		}
	}

	return hunks
}

func initHunk(projKey, flagKey string) ld.HunkRep {
	return ld.HunkRep{
		ProjKey: projKey,
		FlagKey: flagKey,
		Aliases: []string{},
	}
}

func makeTimestamp() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

// Truncate lines to prevent sending over massive hunks, e.g. a minified file.
// NOTE: We may end up truncating a valid flag key reference. We accept this risk
//       and will handle hunks missing flag key references on the frontend.
func truncateLine(line string) string {
	// len(line) returns number of bytes, not num. characters, but it's a close enough
	// approximation for our purposes
	if len(line) > maxLineCharCount {
		// convert to rune slice so that we don't truncate multibyte unicode characters
		runes := []rune(line)
		return string(runes[0:maxLineCharCount]) + "…"
	} else {
		return line
	}
}

func fatalServiceError(err error, ignoreServiceErrors bool) {
	if ld.IsTransient(err) {
		if ignoreServiceErrors {
			os.Exit(0)
		}
		err = fmt.Errorf("%w\n Add the --ignoreServiceErrors flag to ignore this error", err)
	}
	log.Error.Fatal(err)
}
