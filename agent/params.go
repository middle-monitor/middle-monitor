package main

import (
	"net/url"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParamValues holds the query parameters of a target. A scalar value is a fixed
// parameter; a list expands into one target per value, crossed with the other
// lists.
type ParamValues map[string][]string

// UnmarshalYAML accepts both forms, so `module: http_2xx` and
// `module: [a, b]` can sit side by side in the same block.
func (p *ParamValues) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return errParamsNotAMapping
	}
	out := ParamValues{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		value := node.Content[i+1]
		switch value.Kind {
		case yaml.ScalarNode:
			out[key] = []string{value.Value}
		case yaml.SequenceNode:
			values := make([]string, 0, len(value.Content))
			for _, item := range value.Content {
				values = append(values, item.Value)
			}
			out[key] = values
		default:
			return errParamsNotAMapping
		}
	}
	*p = out
	return nil
}

func mergeParams(target ScrapeTarget) ParamValues {
	merged := ParamValues{}
	for key, values := range target.Params {
		merged[key] = values
	}
	// params_matrix is the same mechanism; it exists so the axis being crossed
	// can be read apart from the fixed parameters.
	for key, values := range target.ParamsMatrix {
		merged[key] = values
	}
	return merged
}

// expandTarget turns one target carrying list parameters into one target per
// combination, each with its own URL, name and labels. Without the labels the
// results of a matrix would be indistinguishable once ingested.
func expandTarget(target ScrapeTarget) ([]ScrapeTarget, error) {
	params := mergeParams(target)
	if len(params) == 0 {
		return []ScrapeTarget{target}, nil
	}

	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	expanded := []ScrapeTarget{}
	for _, combination := range combine(keys, params) {
		built, err := applyParams(target, keys, combination)
		if err != nil {
			return nil, err
		}
		expanded = append(expanded, built)
	}
	return expanded, nil
}

// combine walks the cartesian product of the parameter values, in the order of
// the sorted keys so the generated targets are stable across restarts.
func combine(keys []string, params ParamValues) [][]string {
	combinations := [][]string{{}}
	for _, key := range keys {
		values := params[key]
		if len(values) == 0 {
			values = []string{""}
		}
		next := make([][]string, 0, len(combinations)*len(values))
		for _, prefix := range combinations {
			for _, value := range values {
				row := make([]string, len(prefix), len(prefix)+1)
				copy(row, prefix)
				next = append(next, append(row, value))
			}
		}
		combinations = next
	}
	return combinations
}

func applyParams(target ScrapeTarget, keys, values []string) (ScrapeTarget, error) {
	parsed, err := url.Parse(target.URL)
	if err != nil {
		return ScrapeTarget{}, err
	}
	query := parsed.Query()

	labels := map[string]string{}
	varying := []string{}
	for i, key := range keys {
		query.Set(key, values[i])
		labels[key] = values[i]
		if len(target.Params[key])+len(target.ParamsMatrix[key]) > 1 {
			varying = append(varying, values[i])
		}
	}
	// Labels written by the operator win over the ones derived from a parameter.
	for key, value := range target.Labels {
		labels[key] = value
	}

	parsed.RawQuery = query.Encode()
	built := target
	built.URL = parsed.String()
	built.Labels = labels
	built.Params = nil
	built.ParamsMatrix = nil
	if len(varying) > 0 {
		built.Name = target.Name + "/" + strings.Join(varying, "-")
	}
	return built, nil
}
