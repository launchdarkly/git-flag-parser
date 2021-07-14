package coderefs

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/launchdarkly/ld-find-code-refs/internal/git"
	"github.com/launchdarkly/ld-find-code-refs/internal/helpers"
	"github.com/launchdarkly/ld-find-code-refs/internal/ld"
	"github.com/launchdarkly/ld-find-code-refs/internal/log"
	"github.com/launchdarkly/ld-find-code-refs/internal/validation"
	"github.com/launchdarkly/ld-find-code-refs/internal/version"
	"github.com/launchdarkly/ld-find-code-refs/options"
	"github.com/launchdarkly/ld-find-code-refs/search"
)

const (
	minFlagKeyLen    = 3  // Minimum flag key length helps reduce the number of false positives
	maxProjKeyLength = 20 // Maximum project key length
)

// Scan checks the configured directory for flags base on the options configured for Code References.
func Scan(opts options.Options) {
	dir := opts.Dir
	absPath, err := validation.NormalizeAndValidatePath(dir)
	if err != nil {
		log.Error.Fatalf("could not validate directory option: %s", err)
	}

	log.Info.Printf("absolute directory path: %s", absPath)

	branchName := opts.Branch
	revision := opts.Revision
	var gitClient *git.Client
	if revision == "" {
		gitClient, err = git.NewClient(absPath, branchName)
		if err != nil {
			log.Error.Fatalf("%s", err)
		}
		branchName = gitClient.GitBranch
		revision = gitClient.GitSha
	}

	projKey := opts.ProjKey
	checkProjKey(projKey)

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

	filteredFlags, omittedFlags := filterShortFlagKeys(flags)
	if len(filteredFlags) == 0 {
		log.Info.Printf("no flag keys longer than the minimum flag key length (%v) were found for project: %s, exiting early",
			minFlagKeyLen, projKey)
		os.Exit(0)
	} else if len(omittedFlags) > 0 {
		log.Warning.Printf("omitting %d flags with keys less than minimum (%d)", len(omittedFlags), minFlagKeyLen)
	}

	aliases, err := GenerateAliases(filteredFlags, opts.Aliases, dir)
	if err != nil {
		log.Error.Fatalf("failed to create flag key aliases: %v", err)
	}

	ctxLines := opts.ContextLines
	var updateId *int
	if opts.UpdateSequenceId >= 0 {
		updateIdOption := opts.UpdateSequenceId
		updateId = &updateIdOption
	}

	// Configure delimiters
	delims := []string{`"`, `'`, "`"}
	if opts.Delimiters.DisableDefaults {
		delims = []string{}
	}
	delims = append(delims, opts.Delimiters.Additional...)
	delimString := strings.Join(helpers.Dedupe(delims), "")
	delimiterMap := search.BuildDelimiterList(filteredFlags, delimString)
	refs, err := search.SearchForRefs(projKey, absPath, aliases, ctxLines, delimString, delimiterMap)
	if err != nil {
		log.Error.Fatalf("error searching for flag key references: %s", err)
	}

	branch := ld.BranchRep{
		Name:             strings.TrimPrefix(branchName, "refs/heads/"),
		Head:             revision,
		UpdateSequenceId: updateId,
		SyncTime:         makeTimestamp(),
		References:       refs,
	}

	outDir := opts.OutDir
	if outDir != "" {
		outPath, err := branch.WriteToCSV(outDir, projKey, repoParams.Name, revision)
		if err != nil {
			log.Error.Fatalf("error writing code references to csv: %s", err)
		}
		log.Info.Printf("wrote code references to %s", outPath)
	}

	if opts.Debug {
		branch.PrintReferenceCountTable()
	}

	if isDryRun {
		log.Info.Printf(
			"dry run found %d code references across %d flags and %d files",
			branch.TotalHunkCount(),
			len(filteredFlags),
			len(branch.References),
		)
		return
	}

	log.Info.Printf(
		"sending %d code references across %d flags and %d files to LaunchDarkly for project: %s",
		branch.TotalHunkCount(),
		len(filteredFlags),
		len(branch.References),
		projKey,
	)
	err = ldApi.PutCodeReferenceBranch(branch, repoParams.Name)
	switch {
	case err == ld.BranchUpdateSequenceIdConflictErr:
		if branch.UpdateSequenceId != nil {
			log.Warning.Printf("updateSequenceId (%d) must be greater than previously submitted updateSequenceId", *branch.UpdateSequenceId)
		}
	case err == ld.EntityTooLargeErr:
		log.Error.Fatalf("code reference payload too large for LaunchDarkly API - consider excluding more files with .ldignore")
	case err != nil:
		fatalServiceError(fmt.Errorf("error sending code references to LaunchDarkly: %w", err), ignoreServiceErrors)
	}

	if gitClient != nil {
		lookback := opts.Lookback
		if lookback > 0 {
			missingFlags := []string{}
			for flag, count := range branch.CountByFlag(filteredFlags) {
				if count == 0 {
					missingFlags = append(missingFlags, flag)
				}

			}
			log.Info.Printf("checking if %d flags without references were removed in the last %d commits", len(missingFlags), opts.Lookback)
			removedFlags, err := gitClient.FindExtinctions(projKey, missingFlags, delimString, lookback+1)
			if err != nil {
				log.Warning.Printf("unable to generate flag extinctions: %s", err)
			} else {
				log.Info.Printf("found %d removed flags", len(removedFlags))
			}
			if len(removedFlags) > 0 {
				err = ldApi.PostExtinctionEvents(removedFlags, repoParams.Name, branch.Name)
				if err != nil {
					log.Error.Printf("error sending extinction events to LaunchDarkly: %s", err)
				}
			}
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
}

func Prune(opts options.Options, branches []string) {
	ldApi := ld.InitApiClient(ld.ApiOptions{ApiKey: opts.AccessToken, BaseUri: opts.BaseUri, ProjKey: opts.ProjKey, UserAgent: "LDFindCodeRefs/" + version.Version})
	err := ldApi.PostDeleteBranchesTask(opts.RepoName, branches)
	if err != nil {
		fatalServiceError(err, opts.IgnoreServiceErrors)
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

func makeTimestamp() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

// checkProjKey logs a warning if potential sdk keys or access tokens provided as the project key
func checkProjKey(projKey string) {
	if len(projKey) > maxProjKeyLength {
		if strings.HasPrefix(projKey, "sdk-") {
			log.Warning.Printf("provided projKey (%s) appears to be a LaunchDarkly SDK key", "sdk-xxxx")
		} else if strings.HasPrefix(projKey, "api-") {
			log.Warning.Printf("provided projKey (%s) appears to be a LaunchDarkly API access token", "api-xxxx")
		}
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
