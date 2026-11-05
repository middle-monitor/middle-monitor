package main

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Nagios plugin states, by exit code.
const (
	stateOK       = "ok"
	stateWarning  = "warning"
	stateCritical = "critical"
	stateUnknown  = "unknown"
)

const pluginOutputLimit = 64 * 1024

// Plugins get a fixed environment: the agent's own (API key included) is not
// theirs to read, and the C locale keeps decimal points in the perfdata.
var pluginEnv = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}

// PluginResult is one execution of a check, as the platform receives it.
type PluginResult struct {
	State      string      `json:"state"`
	ExitCode   int         `json:"exit_code"`
	Output     string      `json:"output"`
	LongOutput string      `json:"long_output,omitempty"`
	Perfdata   []PerfDatum `json:"perfdata,omitempty"`
	DurationMS float64     `json:"duration_ms"`
	ExecutedAt time.Time   `json:"executed_at"`
}

// PerfDatum is one 'label'=value[unit];warn;crit;min;max entry. Thresholds are
// ranges in the plugin's own syntax (10:20, @5, ~:3), kept as written.
type PerfDatum struct {
	Label string   `json:"label"`
	Value float64  `json:"value"`
	Unit  string   `json:"unit,omitempty"`
	Warn  string   `json:"warn,omitempty"`
	Crit  string   `json:"crit,omitempty"`
	Min   *float64 `json:"min,omitempty"`
	Max   *float64 `json:"max,omitempty"`
}

// runPlugin executes a command with the variables of one check. Every failure
// to get a plugin answer is an unknown state, never an agent error: the check
// is what failed, and the platform must see it.
func runPlugin(ctx context.Context, command Command, vars map[string][]string) PluginResult {
	started := time.Now()
	result := PluginResult{State: stateUnknown, ExitCode: 3, ExecutedAt: started.UTC()}
	defer func() { result.DurationMS = float64(time.Since(started).Microseconds()) / 1000 }()

	args, err := expandArgs(command.Args, vars)
	if err != nil {
		result.Output = "UNKNOWN - " + err.Error()
		return result
	}

	ctx, cancel := context.WithTimeout(ctx, command.timeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, command.Path, args...)
	cmd.Env = pluginEnv
	// A child the plugin forked can hold the pipes open after the kill.
	cmd.WaitDelay = time.Second
	stdout := &limitedBuffer{limit: pluginOutputLimit}
	stderr := &limitedBuffer{limit: pluginOutputLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr

	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		result.Output = "UNKNOWN - plugin timed out after " + command.timeout().String()
		return result
	}
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		result.Output = "UNKNOWN - " + runErr.Error()
		return result
	}

	output := stdout.String()
	if strings.TrimSpace(output) == "" {
		output = stderr.String()
	}
	result.ExitCode = cmd.ProcessState.ExitCode()
	result.State = stateForExitCode(result.ExitCode)
	result.Output, result.LongOutput, result.Perfdata = parsePluginOutput(output)
	return result
}

// Anything outside 0-3, a signal included, is unknown, as in Nagios.
func stateForExitCode(code int) string {
	switch code {
	case 0:
		return stateOK
	case 1:
		return stateWarning
	case 2:
		return stateCritical
	default:
		return stateUnknown
	}
}

// parsePluginOutput splits the plugin output format: the first line is the
// message, the lines after are long output, and perfdata follows a '|' on the
// first line and on any later line, running to the end.
func parsePluginOutput(output string) (string, string, []PerfDatum) {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	message, perf, _ := strings.Cut(lines[0], "|")

	var long []string
	inPerf := false
	for _, line := range lines[1:] {
		if inPerf {
			perf += " " + line
			continue
		}
		text, more, found := strings.Cut(line, "|")
		long = append(long, text)
		if found {
			perf += " " + more
			inPerf = true
		}
	}
	return strings.TrimSpace(message), strings.TrimSpace(strings.Join(long, "\n")), parsePerfdata(perf)
}

// parsePerfdata skips what it cannot read rather than failing the check: a
// malformed entry from a custom script must not hide its state.
func parsePerfdata(perf string) []PerfDatum {
	var data []PerfDatum
	for _, entry := range splitPerfEntries(perf) {
		if datum, ok := parsePerfEntry(entry); ok {
			data = append(data, datum)
		}
	}
	return data
}

// splitPerfEntries splits on spaces outside single quotes, since a quoted
// label may hold spaces ('used space'=3GB).
func splitPerfEntries(perf string) []string {
	var entries []string
	var current strings.Builder
	quoted := false
	for _, r := range perf {
		switch {
		case r == '\'':
			quoted = !quoted
			current.WriteRune(r)
		case r == ' ' && !quoted:
			if current.Len() > 0 {
				entries = append(entries, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		entries = append(entries, current.String())
	}
	return entries
}

func parsePerfEntry(entry string) (PerfDatum, bool) {
	eq := strings.LastIndex(entry, "=")
	if eq <= 0 {
		return PerfDatum{}, false
	}
	label := entry[:eq]
	if len(label) >= 2 && strings.HasPrefix(label, "'") && strings.HasSuffix(label, "'") {
		label = strings.ReplaceAll(label[1:len(label)-1], "''", "'")
	}
	fields := strings.Split(entry[eq+1:], ";")
	value, unit, ok := splitValueUnit(fields[0])
	if !ok {
		// "U" is the plugin saying the value could not be determined.
		return PerfDatum{}, false
	}
	datum := PerfDatum{Label: label, Value: value, Unit: unit}
	if len(fields) > 1 {
		datum.Warn = fields[1]
	}
	if len(fields) > 2 {
		datum.Crit = fields[2]
	}
	if len(fields) > 3 {
		datum.Min = parseOptionalFloat(fields[3])
	}
	if len(fields) > 4 {
		datum.Max = parseOptionalFloat(fields[4])
	}
	return datum, true
}

func splitValueUnit(raw string) (float64, string, bool) {
	end := 0
	for end < len(raw) && strings.ContainsRune("0123456789.-+eE", rune(raw[end])) {
		end++
	}
	// Back off an exponent marker that is really the start of a unit (5e vs 5EB).
	for end > 0 && strings.ContainsRune("eE+-", rune(raw[end-1])) {
		end--
	}
	value, err := strconv.ParseFloat(raw[:end], 64)
	if err != nil {
		return 0, "", false
	}
	return value, raw[end:], true
}

func parseOptionalFloat(raw string) *float64 {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &value
}

// limitedBuffer keeps the first bytes of a stream and discards the rest, so a
// runaway plugin cannot grow the agent's memory. The buffer is not embedded:
// its ReadFrom would let io.Copy bypass the limit.
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }
