package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

const (
	logReadChunk = 64 << 10
	// A record over this is cut: one OTLP message must stay under the receiver's
	// Kafka message limit, and nobody reads a 1 MB line in a log viewer.
	maxLogRecordBytes = 256 << 10
	logPollInterval   = 500 * time.Millisecond
	// A multiline record still open after this is complete: its next line would
	// have arrived with it.
	multilineFlushDelay = time.Second
)

// logRecord is one entry ready to export, with the file offset it ends at so
// the position advances only once the backend has it.
type logRecord struct {
	origin   *logOrigin
	key      string
	path     string
	offset   int64
	time     time.Time
	observed time.Time
	body     string
	severity string
	stream   string
	// Journal position, in place of path and offset for a journald record.
	cursor string
}

// tailer follows one file from an offset until it is gone and read to the end.
type tailer struct {
	key    string
	path   string
	src    *LogSource
	origin *logOrigin
	offset int64
	// Set when the file no longer matches: rotated away or deleted. The tailer
	// still drains what was written before the rotation.
	gone atomic.Bool

	partial strings.Builder
	open    *logRecord
	emit    func(context.Context, logRecord) bool
}

func (t *tailer) run(ctx context.Context, f *os.File) {
	defer f.Close()
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return
	}

	buf := make([]byte, logReadChunk)
	var pending []byte
	for {
		n, err := f.Read(buf)
		if n > 0 {
			var ok bool
			if pending, ok = t.consume(ctx, append(pending, buf[:n]...)); !ok {
				return
			}
			continue
		}
		if err != nil && err != io.EOF {
			return
		}

		t.flushStale(ctx)
		if t.gone.Load() {
			// The writer moved on; a last line without a newline is complete.
			if len(pending) > 0 {
				t.line(ctx, string(pending), t.offset+int64(len(pending)))
			}
			t.flush(ctx)
			return
		}
		// copytruncate rotation: the file shrank under us, start it over.
		if info, err := f.Stat(); err == nil && info.Size() < t.offset+int64(len(pending)) {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return
			}
			t.offset, pending = 0, nil
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(logPollInterval):
		}
	}
}

// consume hands every complete line over and returns the incomplete rest.
// False means the collector stopped.
func (t *tailer) consume(ctx context.Context, pending []byte) ([]byte, bool) {
	for {
		i, next := bytes.IndexByte(pending, '\n'), 1
		if i < 0 {
			if len(pending) <= maxLogRecordBytes {
				return pending, true
			}
			// No newline in sight: cut here, the rest reads as the next line.
			i, next = maxLogRecordBytes, 0
		}
		end := t.offset + int64(i+next)
		if !t.line(ctx, strings.TrimSuffix(string(pending[:i]), "\r"), end) {
			return nil, false
		}
		t.offset = end
		pending = pending[i+next:]
	}
}

// line joins runtime-split pieces and multiline continuations, and emits a
// record once the next one starts. False means the collector stopped.
func (t *tailer) line(ctx context.Context, raw string, end int64) bool {
	decoded := decodeLine(t.src.Type, raw)
	if decoded.partial {
		if t.partial.Len() < maxLogRecordBytes {
			t.partial.WriteString(decoded.text)
		}
		return true
	}
	text := decoded.text
	if t.partial.Len() > 0 {
		t.partial.WriteString(text)
		text = t.partial.String()
		t.partial.Reset()
	}

	if t.open != nil && t.origin.multiline != nil && !t.origin.multiline.MatchString(text) {
		if len(t.open.body) < maxLogRecordBytes {
			t.open.body += "\n" + text
		}
		t.open.offset = end
		t.open.observed = time.Now()
		return true
	}
	if !t.flush(ctx) {
		return false
	}
	t.open = &logRecord{
		origin: t.origin, key: t.key, path: t.path, offset: end,
		time: decoded.time, observed: time.Now(), body: text, stream: decoded.stream,
	}
	if t.origin.multiline == nil {
		return t.flush(ctx)
	}
	return true
}

func (t *tailer) flush(ctx context.Context) bool {
	if t.open == nil {
		return true
	}
	rec := *t.open
	t.open = nil
	if len(rec.body) > maxLogRecordBytes {
		rec.body = rec.body[:maxLogRecordBytes]
	}
	return t.emit(ctx, rec)
}

func (t *tailer) flushStale(ctx context.Context) {
	if t.open != nil && time.Since(t.open.observed) >= multilineFlushDelay {
		t.flush(ctx)
	}
}
