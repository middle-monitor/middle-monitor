package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const journalRestartDelay = 10 * time.Second

// Overridden by tests, which have no systemd.
var journalctlPath = "journalctl"

// Syslog priorities, 0 (emerg) to 7 (debug), as the Logs view levels.
var journalSeverities = map[string]string{
	"0": "FATAL", "1": "ERROR", "2": "ERROR", "3": "ERROR",
	"4": "WARN", "5": "INFO", "6": "INFO", "7": "DEBUG",
}

// journalReader follows the systemd journal through journalctl, which keeps the
// agent free of a cgo binding to libsystemd.
type journalReader struct {
	key      string
	src      *LogSource
	hostname string
	// Last entry read; journalctl resumes after it when it is restarted.
	cursor  string
	origins map[string]*logOrigin
	emit    func(context.Context, logRecord) bool
}

func (j *journalReader) run(ctx context.Context) {
	for {
		err := j.follow(ctx)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, exec.ErrNotFound) {
			slog.Warn("journalctl not found, journald source disabled")
			return
		}
		slog.Warn("journalctl stopped, restarting", "err", err, "delay", journalRestartDelay)
		select {
		case <-ctx.Done():
			return
		case <-time.After(journalRestartDelay):
		}
	}
}

func (j *journalReader) args() []string {
	args := []string{"--follow", "--output=json"}
	if len(j.src.Path) > 0 {
		args = append(args, "--directory="+j.src.Path[0])
	}
	switch {
	case j.cursor != "":
		args = append(args, "--after-cursor="+j.cursor, "--no-tail")
	case j.src.StartAt == "beginning":
		args = append(args, "--no-tail")
	default:
		args = append(args, "--lines=0")
	}
	return args
}

// follow runs one journalctl until it exits or the collector stops.
func (j *journalReader) follow(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, journalctlPath, j.args()...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%w: %w", errJournalctl, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%w: %w", errJournalctl, err)
	}
	reader := bufio.NewReaderSize(out, logReadChunk)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			rec, ok := j.record(line)
			if ok && !j.emit(ctx, rec) {
				break
			}
		}
		if err != nil {
			break
		}
	}
	cancel()
	_, _ = io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		return &journalctlError{Stderr: strings.TrimSpace(stderr.String()), Err: err}
	}
	return errJournalctl
}

// record turns one journalctl JSON line into a record, or false for an entry
// that is unreadable or filtered out by the source.
func (j *journalReader) record(line []byte) (logRecord, bool) {
	var entry map[string]json.RawMessage
	if json.Unmarshal(line, &entry) != nil {
		return logRecord{}, false
	}
	cursor := journalField(entry["__CURSOR"])
	if cursor == "" {
		return logRecord{}, false
	}
	j.cursor = cursor
	origin := j.origin(journalField(entry["_SYSTEMD_UNIT"]), journalField(entry["SYSLOG_IDENTIFIER"]))
	if origin.skip {
		return logRecord{}, false
	}
	body := journalField(entry["MESSAGE"])
	if len(body) > maxLogRecordBytes {
		body = body[:maxLogRecordBytes]
	}
	rec := logRecord{
		origin: origin, key: j.key, cursor: cursor, observed: time.Now(),
		body: body, severity: journalSeverities[journalField(entry["PRIORITY"])],
	}
	if usec, err := strconv.ParseInt(journalField(entry["__REALTIME_TIMESTAMP"]), 10, 64); err == nil {
		rec.time = time.UnixMicro(usec)
	}
	return rec, true
}

// origin keeps one origin per unit, so a batch carries one resource per unit.
func (j *journalReader) origin(unit, identifier string) *logOrigin {
	name := firstNonEmpty(unit, identifier, "journald")
	if origin, ok := j.origins[name]; ok {
		return origin
	}
	origin := &logOrigin{name: name, service: strings.TrimSuffix(name, ".service"), attrs: map[string]string{}}
	if unit != "" {
		origin.attrs["systemd.unit"] = unit
	}
	origin = finishOrigin(j.src, origin, j.hostname)
	j.origins[name] = origin
	return origin
}

// journalField reads a journalctl JSON value: a string, the bytes of a value
// that is not valid UTF-8, or the first of a field set several times.
func journalField(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var octets []int
	if json.Unmarshal(raw, &octets) == nil {
		buf := make([]byte, len(octets))
		for i, b := range octets {
			buf[i] = byte(b)
		}
		return strings.ToValidUTF8(string(buf), "�")
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) == nil && len(values) > 0 {
		return journalField(values[0])
	}
	return ""
}
