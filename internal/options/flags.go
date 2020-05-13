package options

type flag struct {
	name         string
	short        string
	defaultValue interface{}
	usage        string
}

// Options that are available as command line flags
var flags = []flag{
	{
		name:         "accessToken",
		short:        "t",
		defaultValue: "",
		usage:        "LaunchDarkly personal access token with write-level access.",
	},
	{
		name:         "baseUri",
		short:        "U",
		defaultValue: "https://app.launchdarkly.com",
		usage:        "LaunchDarkly base URI.",
	},
	{
		name:         "branch",
		short:        "b",
		defaultValue: "",
		usage: `The currently checked out git branch. If not provided, branch
name will be auto-detected. Provide this option when using CI systems that
leave the repository in a detached HEAD state.`,
	},
	{
		name:         "commitUrlTemplate",
		defaultValue: "",
		usage: `If provided, LaunchDarkly will attempt to generate links to
your Git service provider per commit.
Example: https://github.com/launchdarkly/ld-find-code-refs/commit/${sha}.
Allowed template variables: 'branchName', 'sha'. If commitUrlTemplate is
not provided, but repoUrl is provided and repoType is not custom,
LaunchDarkly will automatically generate links to the repository for each commit.`,
	},
	{
		name:         "contextLines",
		short:        "c",
		defaultValue: 2,
		usage: `The number of context lines to send to LaunchDarkly. If < 0, no
source code will be sent to LaunchDarkly. If 0, only the lines containing
flag references will be sent. If > 0, will send that number of context
lines above and below the flag reference. A maximum of 5 context lines
may be provided.`,
	},
	{
		name:         "debug",
		defaultValue: false,
		usage:        "Enables verbose debug logging",
	},
	{
		name:         "defaultBranch",
		short:        "B",
		defaultValue: "master",
		usage: `The git default branch. The LaunchDarkly UI will default to this branch.
If not provided, will fallback to 'master'.`,
	},
	{
		name:         "dir",
		short:        "d",
		defaultValue: "",
		usage:        "Path to existing checkout of the git repo.",
	},
	{
		name:         "dryRun",
		defaultValue: false,
		usage: `If enabled, the scanner will run without sending code references to
LaunchDarkly. Combine with the outDir option to output code references to a CSV.`,
	},
	{
		name:         "hunkUrlTemplate",
		defaultValue: "",
		usage: `If provided, LaunchDarkly will attempt to generate links to 
your Git service provider per code reference. 
Example: https://github.com/launchdarkly/ld-find-code-refs/blob/${sha}/${filePath}#L${lineNumber}.
Allowed template variables: 'sha', 'filePath', 'lineNumber'. If hunkUrlTemplate is not provided, 
but repoUrl is provided and repoType is not custom, LaunchDarkly will automatically generate
links to the repository for each code reference.`,
	},
	{
		name:         "ignoreServiceErrors",
		short:        "i",
		defaultValue: false,
		usage: `If enabled, the scanner will terminate with exit code 0 when the
LaunchDarkly API is unreachable or returns an unexpected response.`,
	},
	{
		name:         "outDir",
		short:        "o",
		defaultValue: "",
		usage: `If provided, will output a csv file containing all code references for
the project to this directory.`,
	},
	{
		name:         "projKey",
		short:        "p",
		defaultValue: "",
		usage:        `LaunchDarkly project key.`,
	},
	{
		name:         "repoName",
		short:        "r",
		defaultValue: "",
		usage: `Git repo name. Will be displayed in LaunchDarkly. Case insensitive.
Repo names must only contain letters, numbers, '.', '_' or '-'."`,
	},
	{
		name:         "repoType",
		short:        "T",
		defaultValue: "custom",
		usage: `The repo service provider. Used to correctly categorize repositories in the
LaunchDarkly UI. Aceptable values: github|bitbucket|custom.`,
	},
	{
		name:         "repoUrl",
		short:        "u",
		defaultValue: "",
		usage: `The display url for the repository. If provided for a github or
bitbucket repository, LaunchDarkly will attempt to automatically generate source code links.`,
	},
	{
		name:         "updateSequenceId",
		short:        "s",
		defaultValue: -1,
		usage: `An integer representing the order number of code reference updates.
Used to version updates across concurrent executions of the flag finder.
If not provided, data will always be updated. If provided, data will
only be updated if the existing "updateSequenceId" is less than the new
"updateSequenceId". Examples: the time a "git push" was initiated, CI
build number, the current unix timestamp.`,
	},
}
