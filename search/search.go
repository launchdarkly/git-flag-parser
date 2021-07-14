package search

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/launchdarkly/ld-find-code-refs/internal/helpers"
	"github.com/launchdarkly/ld-find-code-refs/internal/ld"
)

const (
	// These are defensive limits intended to prevent corner cases stemming from
	// large repos, false positives, etc. The goal is a) to prevent the program
	// from taking a very long time to run and b) to prevent the program from
	// PUTing a massive json payload. These limits will likely be tweaked over
	// time. The LaunchDarkly backend will also apply limits.
	maxFileCount     = 10000 // Maximum number of files containing code references
	maxHunkCount     = 25000 // Maximum number of total code references
	maxLineCharCount = 500   // Maximum number of characters per line
)

// Truncate lines to prevent sending over massive hunks, e.g. a minified file.
// NOTE: We may end up truncating a valid flag key reference. We accept this risk
//       and will handle hunks missing flag key references on the frontend.
func truncateLine(line string) string {
	// len(line) returns number of bytes, not num. characters, but it's a close enough
	// approximation for our purposes
	if len(line) <= maxLineCharCount {
		return line
	}
	// convert to rune slice so that we don't truncate multibyte unicode characters
	runes := []rune(line)
	return string(runes[0:maxLineCharCount]) + "…"
}

// MatchDelimiters returns true if the given line contains the flag key surrounded by any delimiters
func MatchDelimiters(line, flagKey, delimiters string) bool {
	if delimiters == "" && strings.Contains(line, flagKey) {
		return true
	}
	for _, left := range delimiters {
		for _, right := range delimiters {
			var sb strings.Builder
			sb.Grow(len(flagKey) + 2)
			sb.WriteRune(left)
			sb.WriteString(flagKey)
			sb.WriteRune(right)
			if strings.Contains(line, sb.String()) {
				return true
			}
		}
	}
	return false
}

func MatchDelimitersMap(line, flagKey string, delimiters string, delimiterFlags map[string][]string) bool {
	if delimiters == "" && strings.Contains(line, flagKey) {
		return true
	}

	delimitedFlag := delimiterFlags[flagKey]

	for _, flagKey := range delimitedFlag {
		if strings.Contains(line, flagKey) {
			return true
		}
	}
	return false
}

func BuildDelimiterList(flags []string, delimiters string) map[string][]string {
	delimiterMap := make(map[string][]string)
	if delimiters == "" {
		return delimiterMap
	}
	for _, flag := range flags {
		//flagsDelimited := []string{}
		tempFlags := []string{}
		for _, left := range delimiters {
			for _, right := range delimiters {
				var sb strings.Builder
				sb.Grow(len(flag) + 2)
				sb.WriteRune(left)
				sb.WriteString(flag)
				sb.WriteRune(right)
				tempFlags = append(tempFlags, sb.String())
			}
		}
		delimiterMap[flag] = tempFlags
	}
	return delimiterMap
}

type file struct {
	path  string
	lines []string
}

// hunkForLine returns a matching code reference for a given flag key on a line
func (f file) hunkForLine(projKey, flagKey string, aliases []string, lineNum, ctxLines int, delimiters string, delimiterMap map[string][]string) *ld.HunkRep {
	matchedFlag := false
	aliasMatches := []string{}
	line := f.lines[lineNum]
	// Match flag keys with delimiters
	// if MatchDelimiters(line, flagKey, delimiters) {
	// 	matchedFlag = true
	// }

	if MatchDelimitersMap(line, flagKey, delimiters, delimiterMap) {
		matchedFlag = true
	}

	// Match all aliases for the flag key
	for _, alias := range aliases {
		if strings.Contains(line, alias) {
			aliasMatches = append(aliasMatches, alias)
		}
	}

	if !matchedFlag && len(aliasMatches) == 0 {
		return nil
	}

	startingLineNum := lineNum
	var hunkLines []string
	if ctxLines >= 0 {
		startingLineNum -= ctxLines
		if startingLineNum < 0 {
			startingLineNum = 0
		}
		endingLineNum := lineNum + ctxLines + 1
		if endingLineNum >= len(f.lines) {
			hunkLines = f.lines[startingLineNum:]
		} else {
			hunkLines = f.lines[startingLineNum:endingLineNum]
		}
	}

	for i, line := range hunkLines {
		hunkLines[i] = truncateLine(line)
	}

	ret := ld.HunkRep{
		ProjKey:            projKey,
		FlagKey:            flagKey,
		StartingLineNumber: startingLineNum + 1,
		Lines:              strings.Join(hunkLines, "\n"),
		Aliases:            []string{},
	}
	ret.Aliases = helpers.Dedupe(append(ret.Aliases, aliasMatches...))
	return &ret
}

// aggregateHunksForFlag finds all references in a file, and combines matches if their context lines overlap
func (f file) aggregateHunksForFlag(projKey, flagKey string, flagAliases []string, ctxLines int, delimiters string, delimiterMap map[string][]string) []ld.HunkRep {
	hunksForFlag := []ld.HunkRep{}
	for i := range f.lines {
		match := f.hunkForLine(projKey, flagKey, flagAliases, i, ctxLines, delimiters, delimiterMap)
		if match != nil {
			lastHunkIdx := len(hunksForFlag) - 1
			// If the previous hunk overlaps or is adjacent to the current hunk, merge them together
			if lastHunkIdx >= 0 && hunksForFlag[lastHunkIdx].Overlap(*match) >= 0 {
				hunksForFlag = append(hunksForFlag[:lastHunkIdx], mergeHunks(hunksForFlag[lastHunkIdx], *match)...)
			} else {
				hunksForFlag = append(hunksForFlag, *match)
			}
		}
	}
	return hunksForFlag
}

func (f file) toHunks(projKey string, aliases map[string][]string, ctxLines int, delimiters string, delimiterMap map[string][]string) *ld.ReferenceHunksRep {
	hunks := []ld.HunkRep{}
	for flagKey, flagAliases := range aliases {
		hunks = append(hunks, f.aggregateHunksForFlag(projKey, flagKey, flagAliases, ctxLines, delimiters, delimiterMap)...)
	}
	if len(hunks) == 0 {
		return nil
	}
	return &ld.ReferenceHunksRep{Path: f.path, Hunks: hunks}
}

// mergeHunks combines the lines and aliases of two hunks together for a given file
// if the hunks do not overlap, returns each hunk separately
// assumes the startingLineNumber of a is less than b and there is some overlap between the two
func mergeHunks(a, b ld.HunkRep) []ld.HunkRep {
	if a.StartingLineNumber > b.StartingLineNumber {
		a, b = b, a
	}

	aLines := strings.Split(a.Lines, "\n")
	bLines := strings.Split(b.Lines, "\n")
	overlap := a.Overlap(b)
	// no overlap
	if overlap < 0 || len(a.Lines) == 0 && len(b.Lines) == 0 {
		return []ld.HunkRep{a, b}
	} else if overlap >= len(bLines) {
		// subset hunk
		return []ld.HunkRep{a}
	}

	combinedLines := append(aLines, bLines[overlap:]...)
	return []ld.HunkRep{
		{
			StartingLineNumber: a.StartingLineNumber,
			Lines:              strings.Join(combinedLines, "\n"),
			ProjKey:            a.ProjKey,
			FlagKey:            a.FlagKey,
			Aliases:            helpers.Dedupe(append(a.Aliases, b.Aliases...)),
		},
	}
}

// processFiles starts goroutines to process files individually. When all files have completed processing, the references channel is closed to signal completion.
func processFiles(ctx context.Context, files <-chan file, references chan<- ld.ReferenceHunksRep, projKey string, aliases map[string][]string, ctxLines int, delimiters string, delimiterMap map[string][]string) {
	defer close(references)
	w := sync.WaitGroup{}
	for f := range files {
		if ctx.Err() != nil {
			// context cancelled, stop processing files, but let the waitgroup finish organically
			continue
		}
		w.Add(1)
		go func(f file) {
			reference := f.toHunks(projKey, aliases, ctxLines, delimiters, delimiterMap)
			if reference != nil {
				references <- *reference
			}
			w.Done()
		}(f)
	}
	w.Wait()
}

func SearchForRefs(projKey, workspace string, aliases map[string][]string, ctxLines int, delimiters string, delimiterMap map[string][]string) ([]ld.ReferenceHunksRep, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	files := make(chan file)
	references := make(chan ld.ReferenceHunksRep)

	// Start workers to process files asynchronously as they are written to the files channel
	go processFiles(ctx, files, references, projKey, aliases, ctxLines, delimiters, delimiterMap)

	err := readFiles(ctx, files, workspace)
	if err != nil {
		return nil, err
	}

	ret := []ld.ReferenceHunksRep{}

	defer sort.SliceStable(ret, func(i, j int) bool {
		return ret[i].Path < ret[j].Path
	})

	totalHunks := 0
	for reference := range references {
		ret = append(ret, reference)

		// Reached maximum number of files with code references
		if len(ret) >= maxFileCount {
			return ret, nil
		}
		totalHunks += len(reference.Hunks)
		// Reached maximum number of hunks across all files
		if totalHunks > maxHunkCount {
			return ret, nil
		}
	}
	return ret, nil
}
