package search

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/launchdarkly/ld-find-code-refs/internal/ld"
	"github.com/launchdarkly/ld-find-code-refs/internal/log"
	"github.com/stretchr/testify/require"
)

func init() {
	log.Init(true)
}
func TestMain(m *testing.M) {
	log.Init(true)
	os.Exit(m.Run())
}

const (
	testFlagKey     = "someFlag"
	testFlagKey2    = "anotherFlag"
	testFlagAlias   = "some-flag"
	testFlagAlias2  = "some.flag"
	testFlag2Alias  = "another-flag"
	testFlag2Alias2 = "another.flag"

	defaultDelims = `"` + "'`"
)

var (
	aliases = map[string][]string{
		testFlagKey:  {testFlagAlias, testFlagAlias2},
		testFlagKey2: {testFlag2Alias, testFlag2Alias2},
	}

	// Go definition of testdata/fileWithRefs
	testFile = file{
		path:  "fileWithRefs",
		lines: []string{testFlagKey, testFlagKey2, testFlagKey + testFlagKey2, testFlagAlias, testFlag2Alias},
	}
	testResultHunks = []ld.HunkRep{
		makeHunk(1, testFlagKey),
		*withAliases(makeHunkPtr(3, testFlagKey+testFlagKey2, testFlagAlias), testFlagAlias), //combined
		*withFlagKey(makeHunkPtr(2, testFlagKey2, testFlagKey+testFlagKey2), testFlagKey2),   //combined
		*withFlagKey(withAliases(makeHunkPtr(5, testFlag2Alias), testFlag2Alias), testFlagKey2),
	}

	delimitedTestFlagKey = delimit(testFlagKey, `"`)

	defaultDelimsMap = map[string][]string{testFlagKey: []string{delimitedTestFlagKey}}
)

func Test_hunkForLine(t *testing.T) {
	tests := []struct {
		name         string
		ctxLines     int
		lineNum      int
		lines        []string
		flagKey      string
		delimiters   string
		delimiterMap map[string][]string
		want         *ld.HunkRep
	}{
		{
			name:         "does not match flag flag key without delimiters",
			ctxLines:     -1,
			lineNum:      0,
			flagKey:      testFlagKey,
			lines:        []string{testFlagKey},
			delimiters:   defaultDelims,
			delimiterMap: defaultDelimsMap,
			want:         nil,
		},
		{
			name:         "matches flag key with delimiters",
			ctxLines:     0,
			lineNum:      0,
			flagKey:      testFlagKey,
			lines:        []string{delimitedTestFlagKey},
			delimiters:   defaultDelims,
			delimiterMap: defaultDelimsMap,
			want:         makeHunkPtr(1, delimitedTestFlagKey),
		},
		{
			name:         "matches no context lines without delimiters",
			ctxLines:     -1,
			lineNum:      0,
			flagKey:      testFlagKey,
			lines:        []string{testFlagKey},
			delimiterMap: defaultDelimsMap,
			want:         makeHunkPtr(1),
		},
		{
			name:     "matches with alias",
			ctxLines: -1,
			lineNum:  0,
			flagKey:  testFlagKey,
			lines:    []string{testFlagAlias},
			want:     withAliases(makeHunkPtr(1), testFlagAlias),
		},
		{
			name:     "matches with aliases",
			ctxLines: -1,
			lineNum:  0,
			flagKey:  testFlagKey,
			lines:    []string{testFlagAlias + " " + testFlagAlias2},
			want:     withAliases(makeHunkPtr(1), testFlagAlias, testFlagAlias2),
		},
		{
			name:         "matches with line",
			ctxLines:     0,
			lineNum:      1,
			flagKey:      testFlagKey,
			delimiterMap: defaultDelimsMap,
			lines:        []string{"", testFlagKey, ""},
			want:         makeHunkPtr(2, testFlagKey),
		},
		{
			name:         "matches with context lines",
			ctxLines:     1,
			lineNum:      1,
			flagKey:      testFlagKey,
			delimiterMap: defaultDelimsMap,
			lines:        []string{"", testFlagKey, ""},
			want:         makeHunkPtr(1, "", testFlagKey, ""),
		},
		{
			name:         "truncates long line",
			ctxLines:     0,
			lineNum:      0,
			flagKey:      testFlagKey,
			delimiterMap: defaultDelimsMap,
			lines:        []string{testFlagKey + strings.Repeat("a", maxLineCharCount)},
			want:         makeHunkPtr(1, testFlagKey+strings.Repeat("a", maxLineCharCount-len(testFlagKey))+"…"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := file{lines: tt.lines}
			got := f.hunkForLine("default", tt.flagKey, aliases[tt.flagKey], tt.lineNum, tt.ctxLines, tt.delimiters, tt.delimiterMap)
			require.Equal(t, tt.want, got)
		})
	}

}

func Test_aggregateHunksForFlag(t *testing.T) {
	tests := []struct {
		name     string
		ctxLines int
		lines    []string
		aliases  []string
		want     []ld.HunkRep
	}{
		{
			name:     "does not set lines when context lines are disabled",
			ctxLines: -1,
			lines:    []string{delimitedTestFlagKey, delimitedTestFlagKey, delimitedTestFlagKey},
			want: []ld.HunkRep{
				makeHunk(1),
				makeHunk(2),
				makeHunk(3),
			},
		},
		{
			name:     "combines adjacent hunks with no additional context lines",
			ctxLines: 0,
			lines:    []string{delimitedTestFlagKey, delimitedTestFlagKey, delimitedTestFlagKey},
			want: []ld.HunkRep{
				makeHunk(1, delimitedTestFlagKey, delimitedTestFlagKey, delimitedTestFlagKey),
			},
		},
		{
			name:     "combines adjacent hunks",
			ctxLines: 1,
			lines:    []string{delimitedTestFlagKey, "", "", delimitedTestFlagKey, "", "", delimitedTestFlagKey},
			want: []ld.HunkRep{
				makeHunk(1, delimitedTestFlagKey, "", "", delimitedTestFlagKey, "", "", delimitedTestFlagKey),
			},
		},
		{
			name:     "does not combine hunks with no overlap",
			ctxLines: 1,
			lines:    []string{delimitedTestFlagKey, "", "", "", delimitedTestFlagKey, "", "", "", delimitedTestFlagKey},
			want: []ld.HunkRep{
				makeHunk(1, delimitedTestFlagKey, ""),
				makeHunk(4, "", delimitedTestFlagKey, ""),
				makeHunk(8, "", delimitedTestFlagKey),
			},
		},
		{
			name:     "combines overlapping hunks",
			ctxLines: 1,
			lines:    []string{delimitedTestFlagKey, "", delimitedTestFlagKey, "", delimitedTestFlagKey},
			want: []ld.HunkRep{
				makeHunk(1, delimitedTestFlagKey, "", delimitedTestFlagKey, "", delimitedTestFlagKey),
			},
		},
		{
			name:     "combines multiple types of overlaps",
			ctxLines: 1,
			lines:    []string{delimitedTestFlagKey, "", delimitedTestFlagKey, "", delimitedTestFlagKey},
			want: []ld.HunkRep{
				makeHunk(1, delimitedTestFlagKey, "", delimitedTestFlagKey, "", delimitedTestFlagKey),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := file{lines: tt.lines}
			got := f.aggregateHunksForFlag("default", testFlagKey, []string{}, tt.ctxLines, defaultDelims, defaultDelimsMap)
			require.Equal(t, tt.want, got)
		})
	}
}

func Test_mergeHunks(t *testing.T) {
	tests := []struct {
		name  string
		hunk1 ld.HunkRep
		hunk2 ld.HunkRep
		want  []ld.HunkRep
	}{
		{
			name:  "combine adjacent hunks",
			hunk1: makeHunk(1, "a", "b", "c"),
			hunk2: makeHunk(4, "d", "e", "f"),
			want:  []ld.HunkRep{makeHunk(1, "a", "b", "c", "d", "e", "f")},
		},
		{
			name:  "combine overlapping hunks",
			hunk1: makeHunk(1, "a", "b", "c"),
			hunk2: makeHunk(3, "c", "d", "e"),
			want:  []ld.HunkRep{makeHunk(1, "a", "b", "c", "d", "e")},
		},
		{
			name:  "combine overlapping hunks provided in the wrong order",
			hunk1: makeHunk(3, "c", "d", "e"),
			hunk2: makeHunk(1, "a", "b", "c"),
			want:  []ld.HunkRep{makeHunk(1, "a", "b", "c", "d", "e")},
		},
		{
			name:  "combine same hunk",
			hunk1: makeHunk(1, "a", "b", "c"),
			hunk2: makeHunk(1, "a", "b", "c"),
			want:  []ld.HunkRep{makeHunk(1, "a", "b", "c")},
		},
		{
			name:  "combine subset hunk",
			hunk1: makeHunk(1, "a", "b", "c", "d", "e"),
			hunk2: makeHunk(3, "c", "d"),
			want:  []ld.HunkRep{makeHunk(1, "a", "b", "c", "d", "e")},
		},
		{
			// if the hunks do not overlap and are not adjacent, expect just the first hunk to be returned
			name:  "do not combine disjoint hunks",
			hunk1: makeHunk(1, "a", "b", "c"),
			hunk2: makeHunk(5, "e", "f", "g"),
			want:  []ld.HunkRep{makeHunk(1, "a", "b", "c"), makeHunk(5, "e", "f", "g")},
		},
		{
			// if the hunks are provided out of order, expect both hunks to be returned in the correct order
			name:  "do not combine hunks provided out of order",
			hunk1: makeHunk(5, "e", "f", "g"),
			hunk2: makeHunk(1, "a", "b", "c"),
			want:  []ld.HunkRep{makeHunk(1, "a", "b", "c"), makeHunk(5, "e", "f", "g")},
		},
		{
			name:  "does not combine with no context lines",
			hunk1: makeHunk(1),
			hunk2: makeHunk(2),
			want:  []ld.HunkRep{makeHunk(1), makeHunk(2)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeHunks(tt.hunk1, tt.hunk2)
			require.Equal(t, tt.want, got)
		})
	}
}

func Test_toHunks(t *testing.T) {
	f := testFile
	got := f.toHunks("default", aliases, 0, "", defaultDelimsMap)
	require.Equal(t, "fileWithRefs", got.Path)
	require.Equal(t, len(testResultHunks), len(got.Hunks))
	// no hunks should generate no references
	require.Nil(t, f.toHunks("default", nil, 0, defaultDelims, defaultDelimsMap))
}

func Test_processFiles(t *testing.T) {
	f := testFile
	linesCopy := make([]string, len(f.lines))
	copy(linesCopy, f.lines)
	f2 := file{path: f.path + "2", lines: linesCopy}

	files := make(chan file, 3)
	references := make(chan ld.ReferenceHunksRep, 3)
	files <- f
	files <- f2
	files <- file{path: "no-refs"}
	close(files)
	go processFiles(context.Background(), files, references, "default", aliases, 0, "", defaultDelimsMap)
	totalRefs := 0
	totalHunks := 0
	for reference := range references {
		totalRefs++
		totalHunks += len(reference.Hunks)
	}
	require.Equal(t, 2, totalRefs, "The file with no references should not have been added to refs")
	require.Equal(t, 8, totalHunks, "See Test_toHunks for a more comprehensive example of why this should be 4 per file (2 files with the same refs)")
}

func Test_SearchForRefs(t *testing.T) {
	want := []ld.ReferenceHunksRep{{Path: testFile.path}}
	got, err := SearchForRefs("default", "testdata", aliases, 0, "", defaultDelimsMap)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, want[0].Path, got[0].Path)
}

func withAliases(hunk *ld.HunkRep, aliases ...string) *ld.HunkRep {
	hunk.Aliases = aliases
	return hunk
}

func withFlagKey(hunk *ld.HunkRep, flagKey string) *ld.HunkRep {
	hunk.FlagKey = flagKey
	return hunk
}

func makeHunkPtr(startingLineNumber int, lines ...string) *ld.HunkRep {
	hunk := makeHunk(startingLineNumber, lines...)
	return &hunk
}

func makeHunk(startingLineNumber int, lines ...string) ld.HunkRep {
	hunkLines := ""
	if len(lines) != 0 {
		hunkLines = strings.Join(lines, "\n")
	}
	return ld.HunkRep{
		ProjKey:            "default",
		FlagKey:            testFlagKey,
		StartingLineNumber: startingLineNumber,
		Lines:              hunkLines,
		Aliases:            []string{},
	}
}

func delimit(s string, delim string) string {
	return delim + s + delim
}
