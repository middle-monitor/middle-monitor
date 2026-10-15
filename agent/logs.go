package main

import (
	"path/filepath"
	"regexp"
)

const (
	logSourceFile       = "file"
	logSourceDocker     = "docker"
	logSourceKubernetes = "kubernetes"
	logSourceJournald   = "journald"

	defaultLogStateFile = "/var/lib/middle-monitor/log-positions.json"
)

// Where the container runtimes write stdout and stderr on the host. Rotated
// copies (-json.log.1, 0.log.20240101-...) do not match on purpose.
var defaultLogPaths = map[string][]string{
	logSourceDocker:     {"/var/lib/docker/containers/*/*-json.log"},
	logSourceKubernetes: {"/var/log/pods/*/*/*.log"},
}

// LogsConfig tails log files, Docker containers, Kubernetes pods and the systemd
// journal and ships them over OTLP. Off by default.
type LogsConfig struct {
	Enabled bool `yaml:"enabled"`
	// Read positions survive a restart here, so the agent resumes instead of
	// re-sending or skipping lines.
	StateFile string `yaml:"state_file"`
	// 0 means no limit. Over the limit the agent reads slower; it does not drop.
	MaxLinesPerSecond int `yaml:"max_lines_per_second"`
	// Applied to every source, before the source's own rules.
	Rules   []LogRule   `yaml:"processing_rules"`
	Sources []LogSource `yaml:"sources"`
}

type LogSource struct {
	Type string `yaml:"type"`
	// Globs for type file. For docker and kubernetes, overrides the default
	// location (a host directory mounted elsewhere in the agent's container).
	// For journald, the journal directory instead of the system journal.
	Path StringOrList `yaml:"path"`
	// For a container source, wins over the name derived from the container.
	Service string `yaml:"service"`
	// Matched against the container name (docker), namespace/pod/container
	// (kubernetes) or the systemd unit (journald).
	Include string `yaml:"include"`
	Exclude string `yaml:"exclude"`
	// end (default) or beginning, for files already there when the agent starts.
	// Files that appear later are always read from the beginning.
	StartAt string `yaml:"start_at"`
	// A record starts on a line matching this expression; the lines that follow
	// are appended to it, which keeps a stack trace in one record.
	Multiline string    `yaml:"multiline"`
	Rules     []LogRule `yaml:"processing_rules"`

	include, exclude, multiline *regexp.Regexp
}

const (
	logRuleExclude = "exclude_at_match"
	logRuleInclude = "include_at_match"
	logRuleMask    = "mask_sequences"
)

type LogRule struct {
	Type    string `yaml:"type"`
	Name    string `yaml:"name"`
	Pattern string `yaml:"pattern"`
	Replace string `yaml:"replace_placeholder"`

	re *regexp.Regexp
}

// PrepareLogs validates the logs block and compiles its expressions, so a typo
// fails the config load and --config-check rather than a tailer at runtime.
func PrepareLogs(cfg *LogsConfig) error {
	if cfg.StateFile == "" {
		cfg.StateFile = defaultLogStateFile
	}
	if cfg.MaxLinesPerSecond < 0 {
		return errLogRateNegative
	}
	if err := prepareLogRules(cfg.Rules, "logs.processing_rules"); err != nil {
		return err
	}
	for i := range cfg.Sources {
		if err := prepareLogSource(&cfg.Sources[i]); err != nil {
			return &logSourceError{Index: i, Err: err}
		}
	}
	return nil
}

func prepareLogSource(src *LogSource) error {
	switch src.Type {
	case logSourceFile:
		if len(src.Path) == 0 {
			return errLogPathMissing
		}
	case logSourceDocker, logSourceKubernetes:
		if len(src.Path) == 0 {
			src.Path = defaultLogPaths[src.Type]
		}
	case logSourceJournald:
		// Path is the journal directory, for a journal mounted from the host.
		if len(src.Path) > 1 {
			return errLogJournalPath
		}
	default:
		return errLogSourceType
	}
	for _, pattern := range src.Path {
		if _, err := filepath.Match(pattern, ""); err != nil {
			return &includeError{Path: pattern, Err: err}
		}
	}
	if src.StartAt != "" && src.StartAt != "end" && src.StartAt != "beginning" {
		return errLogStartAt
	}
	var err error
	if src.include, err = compileOptional("include", src.Include); err != nil {
		return err
	}
	if src.exclude, err = compileOptional("exclude", src.Exclude); err != nil {
		return err
	}
	if src.multiline, err = compileOptional("multiline", src.Multiline); err != nil {
		return err
	}
	return prepareLogRules(src.Rules, "processing_rules")
}

func prepareLogRules(rules []LogRule, field string) error {
	for i := range rules {
		rule := &rules[i]
		switch rule.Type {
		case logRuleExclude, logRuleInclude, logRuleMask:
		default:
			return errLogRuleType
		}
		if rule.Pattern == "" {
			return errLogRulePattern
		}
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return &regexpError{Field: field, Expr: rule.Pattern, Err: err}
		}
		rule.re = re
	}
	return nil
}

func compileOptional(field, expr string) (*regexp.Regexp, error) {
	if expr == "" {
		return nil, nil
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, &regexpError{Field: field, Expr: expr, Err: err}
	}
	return re, nil
}

// applyLogRules returns the line after masking, or false when a rule drops it.
func applyLogRules(rules []LogRule, line string) (string, bool) {
	for _, rule := range rules {
		switch rule.Type {
		case logRuleExclude:
			if rule.re.MatchString(line) {
				return "", false
			}
		case logRuleInclude:
			if !rule.re.MatchString(line) {
				return "", false
			}
		case logRuleMask:
			line = rule.re.ReplaceAllString(line, rule.Replace)
		}
	}
	return line, true
}
