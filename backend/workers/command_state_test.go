package workers

import (
	"testing"
	"time"

	"middle-monitor/backend/models"
)

var commandT0 = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

func commandResult(state string, minute int) models.CommandResult {
	return models.CommandResult{ServiceID: 1, State: state, ExecutedAt: commandT0.Add(time.Duration(minute) * time.Minute)}
}

// apply runs results through the state machine and returns every state.
func apply(t *testing.T, maxAttempts int, states ...string) []commandCheckState {
	t.Helper()
	var prev *commandCheckState
	var out []commandCheckState
	for i, s := range states {
		next, ok := nextCommandState(prev, commandResult(s, i), maxAttempts)
		if !ok {
			t.Fatalf("result %d was not applied", i)
		}
		out = append(out, next)
		prev = &next
	}
	return out
}

// One failed NRPE poll during a network blip must not page anyone: the
// problem only becomes hard once it has failed max_attempts times in a row.
func TestProblemTurnsHardAfterMaxAttempts(t *testing.T) {
	got := apply(t, 3, "ok", "critical", "critical", "critical")
	if got[1].StateType != stateTypeSoft || got[1].HardState != commandStateOK {
		t.Fatalf("first failure %+v, want soft with hard state ok", got[1])
	}
	if got[2].StateType != stateTypeSoft {
		t.Fatalf("second failure %+v, want still soft", got[2])
	}
	if got[3].StateType != stateTypeHard || got[3].HardState != commandStateCritical || got[3].Attempt != 3 {
		t.Fatalf("third failure %+v, want hard critical at attempt 3", got[3])
	}
}

// A blip that recovers before max_attempts never changed the hard state, so
// it must neither alert nor send a recovery.
func TestSoftProblemRecoveringLeavesHardStateAlone(t *testing.T) {
	got := apply(t, 3, "ok", "critical", "ok")
	for i, s := range got {
		if s.HardState != commandStateOK {
			t.Fatalf("state %d: hard state %q, want ok throughout", i, s.HardState)
		}
	}
	if got[2].Attempt != 1 {
		t.Fatalf("attempt %d after recovery, want the counter reset", got[2].Attempt)
	}
}

// After a confirmed outage the on-call must hear about the recovery at once,
// not after three more polls.
func TestRecoveryFromHardProblemIsImmediate(t *testing.T) {
	got := apply(t, 3, "critical", "critical", "critical", "ok")
	if got[3].HardState != commandStateOK || got[3].LastHardStateChange != commandT0.Add(3*time.Minute) {
		t.Fatalf("recovery %+v, want hard ok at the recovering result", got[3])
	}
}

// warning -> critical on an already confirmed problem is a real escalation
// and must not wait for new attempts.
func TestEscalationOfHardProblemIsImmediate(t *testing.T) {
	got := apply(t, 3, "warning", "warning", "warning", "critical")
	if got[3].HardState != commandStateCritical {
		t.Fatalf("escalation %+v, want hard critical at once", got[3])
	}
}

// A soft warning that turns critical keeps counting: the outage is the same.
func TestAttemptsCountAcrossProblemStates(t *testing.T) {
	got := apply(t, 3, "ok", "warning", "critical", "critical")
	if got[3].HardState != commandStateCritical {
		t.Fatalf("got %+v, want hard critical on the third problem in a row", got[3])
	}
}

func TestMaxAttemptsOneIsHardAtOnce(t *testing.T) {
	got := apply(t, 1, "critical")
	if got[0].StateType != stateTypeHard || got[0].HardState != commandStateCritical {
		t.Fatalf("got %+v, want hard critical at once", got[0])
	}
}

// A retried batch or a collector failover can replay an old result; applying
// it would flip the state back in time.
func TestOlderResultIsIgnored(t *testing.T) {
	prev := commandCheckState{State: commandStateOK, StateType: stateTypeHard, Attempt: 1, HardState: commandStateOK, ExecutedAt: commandT0.Add(time.Minute)}
	if _, ok := nextCommandState(&prev, commandResult("critical", 0), 3); ok {
		t.Fatal("a result older than the stored state was applied")
	}
	if _, ok := nextCommandState(&prev, commandResult("critical", 1), 3); ok {
		t.Fatal("the same result applied twice")
	}
}

// A state the worker does not know is reported, as unknown, not dropped.
func TestUnexpectedStateIsUnknown(t *testing.T) {
	got := apply(t, 1, "weird")
	if got[0].HardState != commandStateUnknown {
		t.Fatalf("got %q, want unknown", got[0].HardState)
	}
}
