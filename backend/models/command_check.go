package models

import "time"

// CollectorSync is what a zone collector sends on each sync: who it is and the
// commands its local files declare.
type CollectorSync struct {
	Zone     string   `json:"zone"`
	Hostname string   `json:"hostname"`
	Version  string   `json:"version"`
	Commands []string `json:"commands"`
}

// CollectorAssignment is one check a collector must run.
type CollectorAssignment struct {
	ServiceID int64               `json:"id"`
	Name      string              `json:"name"`
	Command   string              `json:"command"`
	Vars      map[string][]string `json:"vars"`
	Interval  int                 `json:"interval"`
}

// CollectorSyncResponse lists the checks of the collector's zone.
type CollectorSyncResponse struct {
	Checks         []CollectorAssignment `json:"checks"`
	RefreshSeconds int                   `json:"refresh_seconds"`
}

// CommandPerfDatum is one perfdata entry of a plugin output.
type CommandPerfDatum struct {
	Label string   `json:"label"`
	Value float64  `json:"value"`
	Unit  string   `json:"unit,omitempty"`
	Warn  string   `json:"warn,omitempty"`
	Crit  string   `json:"crit,omitempty"`
	Min   *float64 `json:"min,omitempty"`
	Max   *float64 `json:"max,omitempty"`
}

// CommandResult is one execution of a command check by a collector.
type CommandResult struct {
	ServiceID  int64              `json:"service_id"`
	State      string             `json:"state"`
	ExitCode   int                `json:"exit_code"`
	Output     string             `json:"output"`
	LongOutput string             `json:"long_output,omitempty"`
	Perfdata   []CommandPerfDatum `json:"perfdata,omitempty"`
	DurationMS float64            `json:"duration_ms"`
	ExecutedAt time.Time          `json:"executed_at"`
}

// CommandResultBatch is what a collector posts, and what travels on Kafka with
// the organization the receiver resolved from the install token.
type CommandResultBatch struct {
	OrganizationID int64           `json:"organization_id"`
	Zone           string          `json:"zone"`
	Hostname       string          `json:"hostname"`
	Results        []CommandResult `json:"results"`
}
