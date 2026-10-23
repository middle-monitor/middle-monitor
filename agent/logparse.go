package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
)

// runtimeLine is one line as a container runtime wrote it, unwrapped.
type runtimeLine struct {
	text   string
	stream string
	time   time.Time
	// The runtime split a long line; the rest follows in the next entries.
	partial bool
}

// decodeLine unwraps the runtime's envelope. A plain file has none.
func decodeLine(sourceType, raw string) runtimeLine {
	switch sourceType {
	case logSourceDocker:
		return decodeDockerJSON(raw)
	case logSourceKubernetes:
		// A node still on the Docker runtime writes json-file under /var/log/pods.
		if strings.HasPrefix(raw, "{") {
			return decodeDockerJSON(raw)
		}
		return decodeCRI(raw)
	}
	return runtimeLine{text: raw}
}

// decodeDockerJSON reads {"log":"...\n","stream":"stdout","time":"..."}. Docker
// cuts lines at 16 KB and only the last piece keeps its newline.
func decodeDockerJSON(raw string) runtimeLine {
	var entry struct {
		Log    string    `json:"log"`
		Stream string    `json:"stream"`
		Time   time.Time `json:"time"`
	}
	if json.Unmarshal([]byte(raw), &entry) != nil {
		return runtimeLine{text: raw}
	}
	text := strings.TrimSuffix(entry.Log, "\n")
	return runtimeLine{
		text:    strings.TrimSuffix(text, "\r"),
		stream:  entry.Stream,
		time:    entry.Time,
		partial: !strings.HasSuffix(entry.Log, "\n"),
	}
}

// decodeCRI reads "<RFC3339Nano> <stream> <P|F> <text>", the format containerd
// and CRI-O write. P marks a line the runtime split.
func decodeCRI(raw string) runtimeLine {
	parts := strings.SplitN(raw, " ", 4)
	if len(parts) < 3 {
		return runtimeLine{text: raw}
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return runtimeLine{text: raw}
	}
	line := runtimeLine{stream: parts[1], time: ts, partial: parts[2] == "P"}
	if len(parts) == 4 {
		line.text = parts[3]
	}
	return line
}

// The severities the Logs view filters on, with their OTLP numbers.
var severityNumbers = map[string]logspb.SeverityNumber{
	"DEBUG": logspb.SeverityNumber_SEVERITY_NUMBER_DEBUG,
	"INFO":  logspb.SeverityNumber_SEVERITY_NUMBER_INFO,
	"WARN":  logspb.SeverityNumber_SEVERITY_NUMBER_WARN,
	"ERROR": logspb.SeverityNumber_SEVERITY_NUMBER_ERROR,
	"FATAL": logspb.SeverityNumber_SEVERITY_NUMBER_FATAL,
}

var severityAliases = map[string]string{
	"TRACE": "DEBUG", "DEBUG": "DEBUG", "DBG": "DEBUG",
	"INFO": "INFO", "INFORMATION": "INFO", "NOTICE": "INFO", "INF": "INFO",
	"WARN": "WARN", "WARNING": "WARN", "WRN": "WARN",
	"ERROR": "ERROR", "ERR": "ERROR", "CRIT": "ERROR", "CRITICAL": "ERROR", "ALERT": "ERROR",
	"FATAL": "FATAL", "PANIC": "FATAL", "EMERG": "FATAL", "EMERGENCY": "FATAL",
}

// Only the head of a line is probed: "no error found" deep in a message is not a
// level. A bare keyword counts only in uppercase for the same reason; between
// brackets, as nginx writes "[crit]", any case is a level.
var (
	logfmtLevelRe  = regexp.MustCompile(`(?i)\b(?:level|lvl|severity)\s*[=:]\s*"?([a-z]+)`)
	bracketLevelRe = regexp.MustCompile(`(?i)\[(trace|debug|info|notice|warn|warning|error|err|crit|critical|alert|emerg|fatal|panic)\]`)
	keywordLevelRe = regexp.MustCompile(`\b(TRACE|DEBUG|DBG|INFO|NOTICE|WARN|WARNING|ERROR|ERR|CRIT|CRITICAL|FATAL|PANIC)\b`)
)

const severityProbeBytes = 200

// detectSeverity reads the level a line declares, JSON first, then logfmt, then
// a bare keyword. A line that declares nothing gets the fallback.
func detectSeverity(fields map[string]interface{}, text, fallback string) string {
	if fields != nil {
		for _, key := range []string{"level", "severity", "lvl", "levelname", "log.level", "loglevel"} {
			if level, ok := normalizeLevel(fields[key]); ok {
				return level
			}
		}
	}
	head := text
	if len(head) > severityProbeBytes {
		head = head[:severityProbeBytes]
	}
	if m := logfmtLevelRe.FindStringSubmatch(head); m != nil {
		if level, ok := severityAliases[strings.ToUpper(m[1])]; ok {
			return level
		}
	}
	if m := bracketLevelRe.FindStringSubmatch(head); m != nil {
		return severityAliases[strings.ToUpper(m[1])]
	}
	if m := keywordLevelRe.FindStringSubmatch(head); m != nil {
		return severityAliases[m[1]]
	}
	return fallback
}

// normalizeLevel accepts a level name or a bunyan/pino number (30 info, 50 error).
func normalizeLevel(value interface{}) (string, bool) {
	switch v := value.(type) {
	case string:
		level, ok := severityAliases[strings.ToUpper(v)]
		return level, ok
	case float64:
		switch {
		case v >= 60:
			return "FATAL", true
		case v >= 50:
			return "ERROR", true
		case v >= 40:
			return "WARN", true
		case v >= 30:
			return "INFO", true
		case v > 0:
			return "DEBUG", true
		}
	}
	return "", false
}

// jsonFields decodes a structured line. Anything else returns nil.
func jsonFields(text string) map[string]interface{} {
	if !strings.HasPrefix(text, "{") {
		return nil
	}
	var fields map[string]interface{}
	if json.Unmarshal([]byte(text), &fields) != nil {
		return nil
	}
	return fields
}

// jsonTime reads the timestamp a structured line carries (GitLab writes "time").
func jsonTime(fields map[string]interface{}) (time.Time, bool) {
	for _, key := range []string{"time", "timestamp", "@timestamp", "ts"} {
		switch v := fields[key].(type) {
		case string:
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				return t, true
			}
		case float64:
			// Epoch seconds or milliseconds, told apart by magnitude.
			if v > 1e12 {
				return time.UnixMilli(int64(v)), true
			}
			if v > 1e9 {
				return time.Unix(0, int64(v*1e9)), true
			}
		}
	}
	return time.Time{}, false
}
