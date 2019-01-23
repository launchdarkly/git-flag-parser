package main

import (
	"flag"
	"os"

	"github.com/launchdarkly/ld-find-code-refs/internal/log"
	o "github.com/launchdarkly/ld-find-code-refs/internal/options"
	"github.com/launchdarkly/ld-find-code-refs/pkg/parse"
)

func main() {
	log.Info.Printf("Setting Bitbucket action env vars")

	options := map[string]string{
		"repoType":         "bitbucket",
		"repoName":         os.Getenv("BITBUCKET_REPO_SLUG"),
		"dir":              os.Getenv("BITBUCKET_CLONE_DIR"),
		"repoUrl":          os.Getenv("BITBUCKET_GIT_HTTP_ORIGIN"),
		"updateSequenceId": os.Getenv("BITBUCKET_BUILD_NUMBER"),
	}
	ldOptions, err := o.GetLDOptionsFromEnv()
	if err != nil {
		log.Error.Fatalf("Error setting options %s", err)
	}
	for k, v := range ldOptions {
		options[k] = v
	}

	o.Populate()
	for k, v := range options {
		err := flag.Set(k, v)
		if err != nil {
			log.Error.Fatalf("Error setting option %s: %s", k, err)
		}
	}
	log.Info.Printf("Starting repo parsing program with options:\n %+v\n", options)
	parse.Parse()
}
