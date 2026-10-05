package main

import (
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// StringOrList accepts a single value or a list, so `include: a.yaml` and
// `include: [a.yaml, b.yaml]` both work.
type StringOrList []string

func (s *StringOrList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		*s = StringOrList{node.Value}
		return nil
	case yaml.SequenceNode:
		out := make(StringOrList, 0, len(node.Content))
		for _, item := range node.Content {
			out = append(out, item.Value)
		}
		*s = out
		return nil
	default:
		return errIncludeNotAPath
	}
}

// scrapeFragment is one file dropped in an include directory. Both a bare list
// of targets and a `targets:` mapping are accepted: config-management roles
// write one or the other and neither is wrong.
type scrapeFragment struct {
	Targets []ScrapeTarget
}

func (f *scrapeFragment) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		return node.Decode(&f.Targets)
	case yaml.MappingNode:
		var wrapper struct {
			Targets []ScrapeTarget `yaml:"targets"`
		}
		if err := node.Decode(&wrapper); err != nil {
			return err
		}
		f.Targets = wrapper.Targets
		return nil
	default:
		return errFragmentShape
	}
}

// loadIncludes reads every file matched by the include patterns. A pattern that
// matches nothing is not an error: a role installs its fragment when it installs
// its exporter, and the agent is deployed before either.
func loadIncludes(patterns StringOrList) ([]ScrapeTarget, error) {
	targets := []ScrapeTarget{}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, &includeError{Path: pattern, Err: err}
		}
		sort.Strings(matches)
		for _, path := range matches {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, &includeError{Path: path, Err: err}
			}
			var fragment scrapeFragment
			if err := yaml.Unmarshal(data, &fragment); err != nil {
				return nil, &includeError{Path: path, Err: err}
			}
			targets = append(targets, fragment.Targets...)
		}
	}
	return targets, nil
}

// dedupByName keeps the first target of a given name. The main config is read
// before the fragments, so a fragment cannot silently replace a target an
// operator wrote by hand.
func dedupByName(targets []ScrapeTarget) []ScrapeTarget {
	seen := make(map[string]bool, len(targets))
	kept := make([]ScrapeTarget, 0, len(targets))
	for _, target := range targets {
		if target.Name != "" {
			if seen[target.Name] {
				slog.Warn("scrape duplicate target ignored", "target", target.Name)
				continue
			}
			seen[target.Name] = true
		}
		kept = append(kept, target)
	}
	return kept
}

// PrepareScrape resolves the config into the target list the manager runs:
// fragments merged, parameters expanded, global labels applied. It returns the
// first configuration error it finds, which is what --config-check reports.
func PrepareScrape(cfg *ScrapeConfig) error {
	included, err := loadIncludes(cfg.Include)
	if err != nil {
		return err
	}

	merged := dedupByName(append(append([]ScrapeTarget{}, cfg.Targets...), included...))

	expanded := []ScrapeTarget{}
	for _, target := range merged {
		targets, err := expandTarget(target)
		if err != nil {
			return &targetError{Name: target.Name, Err: err}
		}
		expanded = append(expanded, targets...)
	}

	for i := range expanded {
		expanded[i].Labels = withGlobalLabels(cfg.Labels, expanded[i].Labels)
		if err := validateTarget(*cfg, expanded[i]); err != nil {
			return &targetError{Name: expanded[i].Name, Err: err}
		}
	}

	cfg.Targets = expanded
	return validateDiscovery(*cfg)
}

// withGlobalLabels applies the agent-wide labels without overriding a label the
// target already sets.
func withGlobalLabels(global, target map[string]string) map[string]string {
	if len(global) == 0 {
		return target
	}
	merged := make(map[string]string, len(global)+len(target))
	for key, value := range global {
		merged[key] = value
	}
	for key, value := range target {
		merged[key] = value
	}
	return merged
}

func validateTarget(cfg ScrapeConfig, target ScrapeTarget) error {
	if target.URL == "" {
		return errTargetURLMissing
	}
	parsed, err := url.Parse(target.URL)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errTargetSchemeUnsupported
	}
	if _, err := target.compileFilter(); err != nil {
		return err
	}
	// Building the client here is what turns an unreadable CA file into a
	// config error instead of a scrape that fails every interval.
	if _, err := clientFor(cfg, target); err != nil {
		return err
	}
	return nil
}

func validateDiscovery(cfg ScrapeConfig) error {
	for _, nomad := range cfg.Nomad {
		if _, err := nomad.compile(); err != nil {
			return err
		}
	}
	for _, httpSD := range cfg.HTTPSD {
		if httpSD.URL == "" {
			return errHTTPSDURLMissing
		}
	}
	return nil
}
