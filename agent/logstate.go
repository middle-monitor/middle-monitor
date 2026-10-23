package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

type logPosition struct {
	Path   string `json:"path,omitempty"`
	Offset int64  `json:"offset,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// logPositions holds, per file, the offset up to which the backend accepted the
// records. A record not yet accepted is read again after a restart.
type logPositions struct {
	mu      sync.Mutex
	path    string
	entries map[string]logPosition
	dirty   bool
	warned  bool
}

func loadLogPositions(path string) *logPositions {
	p := &logPositions{path: path, entries: map[string]logPosition{}}
	data, err := os.ReadFile(path)
	if err != nil {
		return p
	}
	if err := json.Unmarshal(data, &p.entries); err != nil {
		slog.Warn("log positions unreadable, starting over", "path", path, "err", err)
		p.entries = map[string]logPosition{}
	}
	return p
}

func (p *logPositions) get(key string) (logPosition, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pos, ok := p.entries[key]
	return pos, ok
}

func (p *logPositions) ack(rec logRecord) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if rec.cursor != "" {
		p.entries[rec.key] = logPosition{Cursor: rec.cursor}
		p.dirty = true
		return
	}
	if current, ok := p.entries[rec.key]; ok && current.Offset >= rec.offset && current.Path == rec.path {
		return
	}
	p.entries[rec.key] = logPosition{Path: rec.path, Offset: rec.offset}
	p.dirty = true
}

// save writes the positions of the files still tailed; the others are gone.
func (p *logPositions) save(active map[string]bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key := range p.entries {
		if !active[key] {
			delete(p.entries, key)
			p.dirty = true
		}
	}
	if !p.dirty {
		return
	}
	if err := p.write(); err != nil {
		// Collection goes on; only a restart would re-send or skip lines.
		if !p.warned {
			slog.Warn("save log positions failed", "path", p.path, "err", err)
			p.warned = true
		}
		return
	}
	p.dirty = false
}

// write replaces the file in one rename, so a crash never leaves half of it.
func (p *logPositions) write() error {
	data, err := json.Marshal(p.entries)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}
