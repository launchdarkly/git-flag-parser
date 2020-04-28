package options

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/iancoleman/strcase"
	"gopkg.in/yaml.v2"

	"github.com/launchdarkly/ld-find-code-refs/internal/helpers"
	"github.com/launchdarkly/ld-find-code-refs/internal/validation"
)

// We're using multiple packages for configuration.
// TODO: Use spf13/viper to standardize configuration for args, env, and yaml.

type AliasType string

func (a AliasType) IsValid() error {
	switch a {
	case Literal, CamelCase, PascalCase, SnakeCase, UpperSnakeCase, KebabCase, DotCase, FilePattern, Command:
		return nil
	}
	return fmt.Errorf("'%s' is not a valid alias type", a)
}

func (t AliasType) unexpectedFieldErr(field string) error {
	return fmt.Errorf("unexpected field for %s alias: '%s'", t, field)
}

func (a Alias) Generate(flag string) ([]string, error) {
	ret := []string{}
	switch AliasType(strings.ToLower(string(a.Type))) {
	case Literal:
		ret = a.Flags[flag]
	case CamelCase:
		ret = []string{strcase.ToLowerCamel(flag)}
	case PascalCase:
		ret = []string{strcase.ToCamel(flag)}
	case SnakeCase:
		ret = []string{strcase.ToSnake(flag)}
	case UpperSnakeCase:
		ret = []string{strcase.ToScreamingSnake(flag)}
	case KebabCase:
		ret = []string{strcase.ToKebab(flag)}
	case DotCase:
		ret = []string{strcase.ToDelimited(flag, '.')}
	case FilePattern:
		pattern := regexp.MustCompile(strings.ReplaceAll(*a.Pattern, "FLAG_KEY", flag))
		results := pattern.FindAllStringSubmatch(string(a.AllFileContents), -1)
		for _, res := range results {
			if len(res) > 1 {
				ret = append(ret, res[1:]...)
			}
		}
	case Command:
		ctx := context.Background()
		if a.Timeout != nil && *a.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, time.Now().Add(time.Second*time.Duration(*a.Timeout)))
			defer cancel()
		}
		tokens := strings.Split(*a.Command, " ")
		name := tokens[0]
		args := []string{}
		if len(tokens) > 1 {
			args = tokens[1:]
		}
		/* #nosec */
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Stdin = strings.NewReader(flag)
		cmd.Dir = Dir.Value()
		stdout, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("failed to execute alias command: %w", err)
		}
		err = json.Unmarshal(stdout, &ret)
		if err != nil {
			return nil, fmt.Errorf("could not unmarshal json output of alias command: %w", err)
		}
	}

	return ret, nil
}

const (
	Literal AliasType = "literal"

	CamelCase      AliasType = "camelcase"
	PascalCase     AliasType = "pascalcase"
	SnakeCase      AliasType = "snakecase"
	UpperSnakeCase AliasType = "uppersnakecase"
	KebabCase      AliasType = "kebabcase"
	DotCase        AliasType = "dotcase"

	FilePattern AliasType = "filepattern"

	Command AliasType = "command"
)

// Alias is a catch-all type for alias configurations
// TODO: can we use OOP?
type Alias struct {
	Type AliasType `yaml:"type"`
	Name string    `yaml:"name"`

	// Literal
	Flags map[string][]string `yaml:"flags,omitempty"`

	// TODO: Remove this field - it has been superceded by Paths and should be removed
	Path *string `yaml:"path"`

	// FilePattern
	Paths           []string `yaml:"paths,omitempty"`
	Pattern         *string  `yaml:"pattern,omitempty"`
	AllFileContents []byte   `yaml:"-"` // data for pattern matching

	// Command
	Command *string `yaml:"command,omitempty"`
	Timeout *int64  `yaml:"timeout,omitempty"`
}

func (a *Alias) IsValid() error {
	err := a.Type.IsValid()
	if err != nil {
		return err
	}
	// Validate expected fields
	switch a.Type {
	case Literal:
		if a.Flags == nil {
			return errors.New("literal aliases must provide an 'flags'")
		}
	case FilePattern:
		if len(a.Paths) == 0 && a.Path == nil {
			return errors.New("filePattern aliases must provide at least one path in 'paths'")
		}
		if a.Pattern == nil {
			return errors.New("filePattern aliases must provide a 'pattern'")
		}
		if !strings.Contains(*a.Pattern, "FLAG_KEY") {
			return errors.New("pattern must contain 'FLAG_KEY' for templating")
		}
		_, err := regexp.Compile(*a.Pattern)
		if err != nil {
			return fmt.Errorf("could not validate regex pattern: %v", err)
		}
	case Command:
		if a.Command == nil {
			return errors.New("command aliases must provide a 'command'")
		}
		if a.Timeout != nil && *a.Timeout < 0 {
			return errors.New("field 'timeout' must be >= 0")
		}
	}

	// Validate unexpected fields
	var unexpectedField string
	switch {
	case a.Type != Literal:
		if a.Flags != nil {
			unexpectedField = "flags"
		}
	case a.Type != FilePattern:
		if len(a.Paths) > 0 {
			unexpectedField = "paths"
		}
		if a.Pattern != nil {
			unexpectedField = "pattern"
		}
	case a.Type != Command:
		if a.Command != nil {
			unexpectedField = "command"
		}
		if a.Timeout != nil {
			unexpectedField = "timeout"
		}
	}
	if unexpectedField != "" {
		return a.Type.unexpectedFieldErr(unexpectedField)
	}

	return nil
}

func (a *Alias) ProcessFileContent(idx int) error {
	if a.Type != FilePattern {
		return nil
	}

	// TODO: Remove this block with release as the Path field is being removed
	if a.Path != nil {
		path := filepath.Join(Dir.Value(), filepath.Dir(*a.Path))
		absPath, err := validation.NormalizeAndValidatePath(path)
		if err != nil {
			return err
		}

		absFile := filepath.Join(absPath, filepath.Base(*a.Path))
		if !validation.FileExists(absFile) {
			return fmt.Errorf("could not find file at path '%s'", absFile)
		}
		/* #nosec */
		data, err := ioutil.ReadFile(absFile)
		if err != nil {
			return fmt.Errorf("could not process file at path '%s': %v", absFile, err)
		}
		a.AllFileContents = data
	}

	aliasId := strconv.Itoa(idx)
	if a.Name != "" {
		aliasId = a.Name
	}

	paths := []string{}
	for _, glob := range a.Paths {
		absGlob := filepath.Join(Dir.Value(), glob)
		matches, err := filepath.Glob(absGlob)
		if err != nil {
			return fmt.Errorf("filePattern '%s': could not process path glob '%s'", aliasId, absGlob)
		}
		paths = append(paths, matches...)
	}
	paths = helpers.Dedupe(paths)
	a.AllFileContents = []byte{}

	for _, path := range paths {
		if !validation.FileExists(path) {
			return fmt.Errorf("filePattern '%s': could not find file at path '%s'", aliasId, path)
		}
		/* #nosec */
		data, err := ioutil.ReadFile(path)
		if err != nil {
			return fmt.Errorf("filePattern '%s': could not process file at path '%s': %v", aliasId, path, err)
		}
		a.AllFileContents = append(a.AllFileContents, data...)
	}
	return nil
}

type YamlOptions struct {
	Aliases []Alias `yaml:"aliases"`
}

func (o *YamlOptions) IsValid() error {
	for _, a := range o.Aliases {
		err := a.IsValid()
		if err != nil {
			return err
		}
	}
	return nil
}

func Yaml() (*YamlOptions, error) {
	pathToYaml := filepath.Join(Dir.Value(), ".launchdarkly/coderefs.yaml")
	if !validation.FileExists(pathToYaml) {
		pathToYaml = filepath.Join(Dir.Value(), ".launchdarkly/coderefs.yml")
		if !validation.FileExists(pathToYaml) {
			return nil, nil
		}
	}

	/* #nosec */
	data, err := ioutil.ReadFile(pathToYaml)
	if err != nil {
		return nil, err
	}

	o := YamlOptions{}
	err = yaml.UnmarshalStrict(data, &o)
	if err != nil {
		return nil, err
	}
	err = o.IsValid()
	if err != nil {
		return nil, err
	}
	for i, a := range o.Aliases {
		err = a.ProcessFileContent(i)
		if err != nil {
			return nil, err
		}
		o.Aliases[i] = a
	}
	return &o, nil
}
