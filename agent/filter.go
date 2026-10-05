package main

import "regexp"

// LabelFilter keeps a series only when the named label matches, for the metrics
// whose name matches Metric. A series whose name matches but whose label does
// not is dropped; a series the rule does not name is untouched.
type LabelFilter struct {
	Metric  string `yaml:"metric"`
	Label   string `yaml:"label"`
	Matches string `yaml:"matches"`
}

type compiledLabelFilter struct {
	metric  *regexp.Regexp
	label   string
	matches *regexp.Regexp
}

// seriesFilter trims a scrape down to the series worth shipping. A single
// node_exporter serves over a thousand of them and a typical setup keeps a few
// dozen, so cutting here means the rest never crosses the network.
type seriesFilter struct {
	keep       []*regexp.Regexp
	drop       []*regexp.Regexp
	keepIf     []compiledLabelFilter
	dropLabels []string
}

func compileAll(field string, exprs []string) ([]*regexp.Regexp, error) {
	if len(exprs) == 0 {
		return nil, nil
	}
	compiled := make([]*regexp.Regexp, 0, len(exprs))
	for _, expr := range exprs {
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, &regexpError{Field: field, Expr: expr, Err: err}
		}
		compiled = append(compiled, re)
	}
	return compiled, nil
}

// compileFilter builds the filter once per target lifetime rather than once per
// scrape.
func (t ScrapeTarget) compileFilter() (*seriesFilter, error) {
	keep, err := compileAll("keep_metrics", t.KeepMetrics)
	if err != nil {
		return nil, err
	}
	drop, err := compileAll("drop_metrics", t.DropMetrics)
	if err != nil {
		return nil, err
	}

	keepIf := make([]compiledLabelFilter, 0, len(t.KeepIfLabels))
	for _, rule := range t.KeepIfLabels {
		if rule.Label == "" {
			return nil, errKeepIfLabelMissing
		}
		metric, err := regexp.Compile(orDefault(rule.Metric, ".*"))
		if err != nil {
			return nil, &regexpError{Field: "keep_if_labels.metric", Expr: rule.Metric, Err: err}
		}
		matches, err := regexp.Compile(rule.Matches)
		if err != nil {
			return nil, &regexpError{Field: "keep_if_labels.matches", Expr: rule.Matches, Err: err}
		}
		keepIf = append(keepIf, compiledLabelFilter{metric: metric, label: rule.Label, matches: matches})
	}

	if len(keep) == 0 && len(drop) == 0 && len(keepIf) == 0 && len(t.DropLabels) == 0 {
		return nil, nil
	}
	return &seriesFilter{keep: keep, drop: drop, keepIf: keepIf, dropLabels: t.DropLabels}, nil
}

func matchesAny(res []*regexp.Regexp, name string) bool {
	for _, re := range res {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}

// apply runs keep_metrics, then drop_metrics, then keep_if_labels, then
// drop_labels. keep_metrics is a whitelist: a non-empty list throws away
// everything it does not name.
func (f *seriesFilter) apply(samples []ScrapedSample) []ScrapedSample {
	if f == nil {
		return samples
	}

	kept := make([]ScrapedSample, 0, len(samples))
	for _, sample := range samples {
		if len(f.keep) > 0 && !matchesAny(f.keep, sample.Name) {
			continue
		}
		if matchesAny(f.drop, sample.Name) {
			continue
		}
		if !f.labelsPass(sample) {
			continue
		}
		for _, label := range f.dropLabels {
			delete(sample.Labels, label)
		}
		kept = append(kept, sample)
	}
	return kept
}

func (f *seriesFilter) labelsPass(sample ScrapedSample) bool {
	for _, rule := range f.keepIf {
		if !rule.metric.MatchString(sample.Name) {
			continue
		}
		// An absent label cannot match, so the series goes: the rule says which
		// dimension is wanted, and this series does not carry it.
		if !rule.matches.MatchString(sample.Labels[rule.label]) {
			return false
		}
	}
	return true
}
