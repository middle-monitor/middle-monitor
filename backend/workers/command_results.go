package workers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

// commandCheck is a check of a batch with its stored state.
type commandCheck struct {
	service models.Service
	state   *commandCheckState
}

// commandTransition is a hard state change, the only thing that alerts.
type commandTransition struct {
	service models.Service
	from    string
	state   commandCheckState
	result  models.CommandResult
}

// processCommandResults applies a collector batch: one read and one write for
// the states, one bulk for the history, and incident work only for the few
// checks whose hard state changed.
func processCommandResults(db *sql.DB, opensearch *services.OpenSearchService, batch models.CommandResultBatch) error {
	if len(batch.Results) == 0 {
		return nil
	}
	checks, err := loadCommandChecks(db, batch)
	if err != nil {
		return err
	}

	// Oldest first, so two results of one check in a batch chain correctly.
	results := append([]models.CommandResult(nil), batch.Results...)
	sort.SliceStable(results, func(i, j int) bool { return results[i].ExecutedAt.Before(results[j].ExecutedAt) })

	latest := map[int64]models.CommandResult{}
	var points []services.SeriesPoint
	var transitions []commandTransition
	for _, result := range results {
		check, ok := checks[result.ServiceID]
		if !ok {
			// Another organization's or zone's check, or one deleted since.
			continue
		}
		previousHard := commandStateOK
		if check.state != nil {
			previousHard = check.state.HardState
		}
		next, applied := nextCommandState(check.state, result, check.service.MaxAttempts)
		if !applied {
			continue
		}
		check.state = &next
		latest[result.ServiceID] = result
		points = append(points, commandSeriesPoints(check.service, batch.Zone, result)...)
		if next.HardState != previousHard {
			transitions = append(transitions, commandTransition{service: check.service, from: previousHard, state: next, result: result})
		}
	}

	if err := saveCommandStates(db, checks, latest, batch.Hostname); err != nil {
		return err
	}
	if opensearch != nil {
		if err := opensearch.IndexSeriesPoints(context.Background(), points); err != nil {
			slog.Error("failed to index command check history", "zone", batch.Zone, "points", len(points), "error", err)
		}
	}
	for _, transition := range transitions {
		applyCommandTransition(db, transition)
	}
	return nil
}

// loadCommandChecks reads the checks of the batch, keeping only those of the
// batch's organization and zone: a collector cannot write another zone's state.
func loadCommandChecks(db *sql.DB, batch models.CommandResultBatch) (map[int64]*commandCheck, error) {
	ids := make([]int64, 0, len(batch.Results))
	for _, result := range batch.Results {
		ids = append(ids, result.ServiceID)
	}
	rows, err := db.Query(`
		SELECT s.id, s.organization_id, s.host_id, s.name, s.host, s.service, s.max_attempts,
		       st.state, st.state_type, st.attempt, st.hard_state,
		       st.executed_at, st.last_state_change, st.last_hard_state_change
		  FROM services s
		  LEFT JOIN command_check_states st ON st.service_id = s.id
		 WHERE s.id = ANY($1) AND s.organization_id = $2 AND s.type = 'command'
		   AND `+services.CommandZoneExpr+` = $3`,
		pq.Array(ids), batch.OrganizationID, batch.Zone)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", services.ErrCommandStateLoad, err)
	}
	defer rows.Close()

	checks := map[int64]*commandCheck{}
	for rows.Next() {
		var svc models.Service
		var state, stateType, hardState sql.NullString
		var attempt sql.NullInt64
		var executedAt, lastChange, lastHardChange sql.NullTime
		if err := rows.Scan(&svc.ID, &svc.OrganizationID, &svc.HostID, &svc.Name, &svc.Host, &svc.Service, &svc.MaxAttempts,
			&state, &stateType, &attempt, &hardState, &executedAt, &lastChange, &lastHardChange); err != nil {
			return nil, fmt.Errorf("%w: %w", services.ErrCommandStateLoad, err)
		}
		svc.Type = services.CommandType
		check := &commandCheck{service: svc}
		if state.Valid {
			check.state = &commandCheckState{
				State: state.String, StateType: stateType.String, Attempt: int(attempt.Int64), HardState: hardState.String,
				ExecutedAt: executedAt.Time, LastStateChange: lastChange.Time, LastHardStateChange: lastHardChange.Time,
			}
		}
		checks[svc.ID] = check
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", services.ErrCommandStateLoad, err)
	}
	return checks, nil
}

// saveCommandStates writes every changed state in one statement. The guard on
// executed_at keeps a newer state when two writers race.
func saveCommandStates(db *sql.DB, checks map[int64]*commandCheck, latest map[int64]models.CommandResult, collector string) error {
	if len(latest) == 0 {
		return nil
	}
	n := len(latest)
	ids, attempts, exitCodes := make([]int64, 0, n), make([]int64, 0, n), make([]int64, 0, n)
	states, stateTypes, hardStates := make([]string, 0, n), make([]string, 0, n), make([]string, 0, n)
	outputs, longOutputs, perfdata := make([]string, 0, n), make([]string, 0, n), make([]string, 0, n)
	durations := make([]float64, 0, n)
	executedAt, lastChange, lastHardChange := make([]string, 0, n), make([]string, 0, n), make([]string, 0, n)

	for id, result := range latest {
		state := checks[id].state
		perf := ""
		if len(result.Perfdata) > 0 {
			encoded, err := json.Marshal(result.Perfdata)
			if err != nil {
				return fmt.Errorf("%w: %w", services.ErrCommandStateSave, err)
			}
			perf = string(encoded)
		}
		ids = append(ids, id)
		states = append(states, state.State)
		stateTypes = append(stateTypes, state.StateType)
		attempts = append(attempts, int64(state.Attempt))
		hardStates = append(hardStates, state.HardState)
		exitCodes = append(exitCodes, int64(result.ExitCode))
		outputs = append(outputs, result.Output)
		longOutputs = append(longOutputs, result.LongOutput)
		perfdata = append(perfdata, perf)
		durations = append(durations, result.DurationMS)
		executedAt = append(executedAt, state.ExecutedAt.UTC().Format(time.RFC3339Nano))
		lastChange = append(lastChange, state.LastStateChange.UTC().Format(time.RFC3339Nano))
		lastHardChange = append(lastHardChange, state.LastHardStateChange.UTC().Format(time.RFC3339Nano))
	}

	_, err := db.Exec(`
		INSERT INTO command_check_states (service_id, state, state_type, attempt, hard_state, exit_code, output,
		       long_output, perfdata, duration_ms, executed_at, last_state_change, last_hard_state_change, collector)
		SELECT u.id, u.state, u.state_type, u.attempt, u.hard_state, u.exit_code, u.output,
		       NULLIF(u.long_output, ''), NULLIF(u.perfdata, '')::jsonb, u.duration_ms, u.executed_at, u.lsc, u.lhsc, $14
		  FROM unnest($1::int[], $2::text[], $3::text[], $4::int[], $5::text[], $6::int[], $7::text[],
		              $8::text[], $9::text[], $10::float8[], $11::timestamptz[], $12::timestamptz[], $13::timestamptz[])
		       AS u(id, state, state_type, attempt, hard_state, exit_code, output, long_output, perfdata, duration_ms, executed_at, lsc, lhsc)
		 WHERE EXISTS (SELECT 1 FROM services s WHERE s.id = u.id)
		ON CONFLICT (service_id) DO UPDATE SET
		       state = EXCLUDED.state, state_type = EXCLUDED.state_type, attempt = EXCLUDED.attempt,
		       hard_state = EXCLUDED.hard_state, exit_code = EXCLUDED.exit_code, output = EXCLUDED.output,
		       long_output = EXCLUDED.long_output, perfdata = EXCLUDED.perfdata, duration_ms = EXCLUDED.duration_ms,
		       executed_at = EXCLUDED.executed_at, last_state_change = EXCLUDED.last_state_change,
		       last_hard_state_change = EXCLUDED.last_hard_state_change, collector = EXCLUDED.collector
		 WHERE command_check_states.executed_at < EXCLUDED.executed_at`,
		pq.Array(ids), pq.Array(states), pq.Array(stateTypes), pq.Array(attempts), pq.Array(hardStates),
		pq.Array(exitCodes), pq.Array(outputs), pq.Array(longOutputs), pq.Array(perfdata), pq.Array(durations),
		pq.Array(executedAt), pq.Array(lastChange), pq.Array(lastHardChange), collector)
	if err != nil {
		return fmt.Errorf("%w: %w", services.ErrCommandStateSave, err)
	}
	return nil
}

// commandSeriesPoints is the history of one result: its state as the Nagios
// exit code, and each perfdata value.
func commandSeriesPoints(svc models.Service, zone string, result models.CommandResult) []services.SeriesPoint {
	labels := map[string]string{"check": svc.Name, "zone": zone}
	points := []services.SeriesPoint{{
		Timestamp: result.ExecutedAt, OrganizationID: svc.OrganizationID, MetricName: "command_state",
		MetricType: "gauge", Value: commandStateValue(normalizeCommandState(result.State)),
		Labels: labels, ServiceName: svc.Service, Hostname: svc.Host,
	}}
	for _, datum := range result.Perfdata {
		perfLabels := map[string]string{"check": svc.Name, "zone": zone, "label": datum.Label}
		points = append(points, services.SeriesPoint{
			Timestamp: result.ExecutedAt, OrganizationID: svc.OrganizationID, MetricName: "command_perfdata",
			MetricType: "gauge", Unit: datum.Unit, Value: datum.Value,
			Labels: perfLabels, ServiceName: svc.Service, Hostname: svc.Host,
		})
	}
	return points
}

// applyCommandTransition opens an incident when a check becomes critical or
// unknown, and resolves it when the check is ok again. A warning neither opens
// one nor counts as a recovery, as for the other checks.
func applyCommandTransition(db *sql.DB, t commandTransition) {
	alertService := services.NewAlertService(db)
	switch t.state.HardState {
	case commandStateOK:
		autoResolveServiceCheckIncident(db, alertService, t.service)
	case commandStateCritical, commandStateUnknown:
		message := t.result.Output
		eventMsg := fmt.Sprintf("Check '%s' is %s after %d attempts", t.service.Name, strings.ToUpper(t.state.HardState), t.state.Attempt)
		alertCheckFailure(db, alertService, t.service, models.ServiceResult{ServiceID: t.service.ID, Status: "failure", Message: &message}, eventMsg)
	}
}
