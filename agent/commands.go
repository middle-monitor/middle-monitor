package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultCommandTimeout = 60 * time.Second

// CollectorConfig turns the agent into a zone collector: it runs the checks the
// platform assigns to its zone, with the commands declared on this machine.
// Off by default.
type CollectorConfig struct {
	Enabled bool   `yaml:"enabled"`
	Zone    string `yaml:"zone"`
	// Globs of files declaring the commands this collector may run.
	Commands StringOrList `yaml:"commands"`

	commands map[string]Command
}

// Command is a plugin the platform may ask for by name. The platform only
// sends variable values: the binary and the shape of its arguments stay here.
type Command struct {
	Path    string   `yaml:"path"`
	Args    []string `yaml:"args"`
	Timeout int      `yaml:"timeout"` // seconds, default 60
}

func (c Command) timeout() time.Duration {
	if c.Timeout > 0 {
		return time.Duration(c.Timeout) * time.Second
	}
	return defaultCommandTimeout
}

type commandsFile struct {
	Commands map[string]Command `yaml:"commands"`
}

var (
	commandNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	// $name$ is one value; a whole argument written $name...$ is a list.
	commandVarPattern  = regexp.MustCompile(`\$([A-Za-z0-9_]+)\$`)
	commandListPattern = regexp.MustCompile(`^\$([A-Za-z0-9_]+)\.\.\.\$$`)
)

// PrepareCollector loads the command files, so a broken definition fails the
// config load and --config-check rather than a check at runtime.
func PrepareCollector(cfg *CollectorConfig) error {
	if !cfg.Enabled {
		return nil
	}
	if cfg.Zone == "" {
		return errCollectorZoneMissing
	}
	commands, err := loadCommands(cfg.Commands)
	if err != nil {
		return err
	}
	cfg.commands = commands
	return nil
}

// loadCommands reads every matched file. The first definition of a name wins,
// so a later file cannot silently swap the binary behind a command.
func loadCommands(patterns StringOrList) (map[string]Command, error) {
	commands := map[string]Command{}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, &includeError{Path: pattern, Err: err}
		}
		sort.Strings(matches)
		for _, path := range matches {
			if err := loadCommandsFile(path, commands); err != nil {
				return nil, err
			}
		}
	}
	return commands, nil
}

func loadCommandsFile(path string, commands map[string]Command) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return &includeError{Path: path, Err: err}
	}
	var file commandsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return &includeError{Path: path, Err: err}
	}
	names := make([]string, 0, len(file.Commands))
	for name := range file.Commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, seen := commands[name]; seen {
			continue
		}
		command := file.Commands[name]
		if err := validateCommand(name, command); err != nil {
			return &includeError{Path: path, Err: err}
		}
		commands[name] = command
	}
	return nil
}

func validateCommand(name string, command Command) error {
	if !commandNamePattern.MatchString(name) {
		return &commandError{Name: name, Err: errCommandName}
	}
	// No PATH lookup: the binary that runs is the one written here.
	if !filepath.IsAbs(command.Path) {
		return &commandError{Name: name, Err: errCommandPathNotAbsolute}
	}
	return nil
}

// expandArgs builds the argument list from the command's template. Each value
// stays one argument: nothing goes through a shell.
func expandArgs(template []string, vars map[string][]string) ([]string, error) {
	args := make([]string, 0, len(template))
	for _, arg := range template {
		if list := commandListPattern.FindStringSubmatch(arg); list != nil {
			values, ok := vars[list[1]]
			if !ok {
				return nil, &commandVarError{Name: list[1], Err: errCommandVarMissing}
			}
			for _, value := range values {
				if err := validateVarValue(list[1], value); err != nil {
					return nil, err
				}
			}
			args = append(args, values...)
			continue
		}
		expanded, err := expandArg(arg, vars)
		if err != nil {
			return nil, err
		}
		args = append(args, expanded)
	}
	return args, nil
}

func expandArg(arg string, vars map[string][]string) (string, error) {
	var expandErr error
	expanded := commandVarPattern.ReplaceAllStringFunc(arg, func(token string) string {
		name := strings.Trim(token, "$")
		values, ok := vars[name]
		if !ok || len(values) != 1 {
			if expandErr == nil {
				expandErr = &commandVarError{Name: name, Err: errCommandVarMissing}
			}
			return ""
		}
		if err := validateVarValue(name, values[0]); err != nil && expandErr == nil {
			expandErr = err
		}
		return values[0]
	})
	return expanded, expandErr
}

// A NUL cannot be passed in argv and the NRPE daemon refuses line breaks;
// failing here names the variable instead of a confusing plugin error.
func validateVarValue(name, value string) error {
	if strings.ContainsAny(value, "\x00\r\n") {
		return &commandVarError{Name: name, Err: errCommandVarInvalid}
	}
	return nil
}
