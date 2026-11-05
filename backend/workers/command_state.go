package workers

import (
	"time"

	"middle-monitor/backend/models"
)

const (
	commandStateOK       = "ok"
	commandStateWarning  = "warning"
	commandStateCritical = "critical"
	commandStateUnknown  = "unknown"

	stateTypeSoft = "soft"
	stateTypeHard = "hard"
)

// commandCheckState is a row of command_check_states.
type commandCheckState struct {
	State               string
	StateType           string
	Attempt             int
	HardState           string
	ExecutedAt          time.Time
	LastStateChange     time.Time
	LastHardStateChange time.Time
}

// nextCommandState applies one result to a check's state with Icinga's
// soft/hard rules: a problem becomes hard, and alerts, only after maxAttempts
// results in a row; a recovery from a hard problem is hard at once. prev is nil
// for a check that never reported. It returns false for a result older than
// the stored one, which a retried batch or a collector failover can deliver.
func nextCommandState(prev *commandCheckState, result models.CommandResult, maxAttempts int) (commandCheckState, bool) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	state := normalizeCommandState(result.State)
	at := result.ExecutedAt

	if prev == nil {
		next := commandCheckState{
			State: state, StateType: stateTypeHard, Attempt: 1, HardState: commandStateOK,
			ExecutedAt: at, LastStateChange: at, LastHardStateChange: at,
		}
		if state != commandStateOK {
			// A first problem counts as the first attempt, not as a hard state.
			next = escalate(next, maxAttempts, at)
		}
		return next, true
	}
	if !at.After(prev.ExecutedAt) {
		return *prev, false
	}

	next := *prev
	next.State = state
	next.ExecutedAt = at
	if state != prev.State {
		next.LastStateChange = at
	}

	switch {
	case state == commandStateOK:
		next.Attempt = 1
		next.StateType = stateTypeHard
		if prev.HardState != commandStateOK {
			next.HardState = commandStateOK
			next.LastHardStateChange = at
		}
	case prev.StateType == stateTypeHard && prev.HardState != commandStateOK:
		// Already a confirmed problem: warning <-> critical moves at once.
		next.Attempt = maxAttempts
		if state != prev.HardState {
			next.HardState = state
			next.LastHardStateChange = at
		}
	default:
		next.Attempt = prev.Attempt + 1
		if prev.State == commandStateOK {
			next.Attempt = 1
		}
		next = escalate(next, maxAttempts, at)
	}
	return next, true
}

// escalate makes a problem hard once it has used its attempts.
func escalate(next commandCheckState, maxAttempts int, at time.Time) commandCheckState {
	if next.Attempt < maxAttempts {
		next.StateType = stateTypeSoft
		return next
	}
	next.Attempt = maxAttempts
	next.StateType = stateTypeHard
	if next.HardState != next.State {
		next.HardState = next.State
		next.LastHardStateChange = at
	}
	return next
}

// Anything the worker does not know is unknown, as for an exit code above 3.
func normalizeCommandState(state string) string {
	switch state {
	case commandStateOK, commandStateWarning, commandStateCritical:
		return state
	default:
		return commandStateUnknown
	}
}

// commandStateValue is the series value of a state, the Nagios exit code.
func commandStateValue(state string) float64 {
	switch state {
	case commandStateOK:
		return 0
	case commandStateWarning:
		return 1
	case commandStateCritical:
		return 2
	default:
		return 3
	}
}
