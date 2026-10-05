package main

import (
	"bufio"
	"io"
	"math"
	"strconv"
	"strings"
)

// ScrapedSample is one datapoint read from an exposition endpoint.
type ScrapedSample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// ParseExposition reads the Prometheus text exposition format.
//
// Histograms and summaries are not expanded: their _bucket, _sum and _count
// lines are already separate series in the format, so they come through as
// ordinary samples. Malformed lines are skipped rather than failing the whole
// scrape — one bad line from an exporter must not cost every other metric.
func ParseExposition(r io.Reader) ([]ScrapedSample, error) {
	samples := []ScrapedSample{}
	scanner := bufio.NewScanner(r)
	// Exporter lines can be long when a metric carries many labels.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sample, ok := parseSampleLine(line)
		if !ok {
			continue
		}
		samples = append(samples, sample)
	}
	if err := scanner.Err(); err != nil {
		return samples, err
	}
	return samples, nil
}

// parseSampleLine reads `name{label="value",...} 12.5 [timestamp]`.
func parseSampleLine(line string) (ScrapedSample, bool) {
	var sample ScrapedSample

	name, rest, ok := splitName(line)
	if !ok {
		return sample, false
	}
	sample.Name = name

	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "{") {
		end := strings.LastIndex(rest, "}")
		if end < 0 {
			return sample, false
		}
		labels, ok := parseLabels(rest[1:end])
		if !ok {
			return sample, false
		}
		sample.Labels = labels
		rest = strings.TrimSpace(rest[end+1:])
	}

	// The value is the first field; an optional timestamp follows and is
	// dropped — the backend stamps points at ingestion.
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return sample, false
	}
	value, ok := parseValue(fields[0])
	if !ok {
		return sample, false
	}
	sample.Value = value
	return sample, true
}

// splitName takes the metric name up to '{' or whitespace.
func splitName(line string) (string, string, bool) {
	for i, r := range line {
		if r == '{' || r == ' ' || r == '\t' {
			name := line[:i]
			if name == "" {
				return "", "", false
			}
			return name, line[i:], true
		}
	}
	return "", "", false
}

// parseLabels reads `a="1",b="2"`, honouring the escapes the format defines.
func parseLabels(raw string) (map[string]string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	labels := map[string]string{}

	for i := 0; i < len(raw); {
		eq := strings.IndexByte(raw[i:], '=')
		if eq < 0 {
			return nil, false
		}
		key := strings.TrimSpace(raw[i : i+eq])
		i += eq + 1
		if i >= len(raw) || raw[i] != '"' {
			return nil, false
		}
		i++ // opening quote

		var value strings.Builder
		closed := false
		for i < len(raw) {
			c := raw[i]
			if c == '\\' && i+1 < len(raw) {
				switch raw[i+1] {
				case 'n':
					value.WriteByte('\n')
				case '"':
					value.WriteByte('"')
				case '\\':
					value.WriteByte('\\')
				default:
					value.WriteByte(raw[i+1])
				}
				i += 2
				continue
			}
			if c == '"' {
				closed = true
				i++
				break
			}
			value.WriteByte(c)
			i++
		}
		if !closed || key == "" {
			return nil, false
		}
		labels[key] = value.String()

		// Skip the separator before the next pair.
		for i < len(raw) && (raw[i] == ',' || raw[i] == ' ') {
			i++
		}
	}
	return labels, true
}

// parseValue accepts the special values the format allows on top of numbers.
func parseValue(raw string) (float64, bool) {
	switch raw {
	case "+Inf":
		return math.Inf(1), true
	case "-Inf":
		return math.Inf(-1), true
	case "NaN":
		// NaN cannot be aggregated or charted; the sample is dropped.
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
