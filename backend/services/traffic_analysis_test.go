package services

import (
	"strings"
	"testing"
)

// Traffic analysis is what turns "your check failed" into "and this service is
// taking 40x its usual load". It is shown as a strong correlation, not a proof,
// so the bar for calling something a signal has to be deliberate: too low and
// every incident gets a confident-sounding but meaningless cause.

func TestIsSignificantTrafficSignal(t *testing.T) {
	cases := []struct {
		name            string
		traceCount      int64
		traceMultiplier float64
		avgLatency      float64
		logCount        int64
		logMultiplier   float64
		errorLogCount   int64
		want            bool
	}{
		// A big multiplier on a handful of requests is noise: three requests
		// where there is usually one is not an event.
		{"few traces, big multiplier", 10, 50, 0, 0, 0, 0, false},
		// Volume alone is not an anomaly either; a busy service is just busy.
		{"many traces, flat multiplier", 5000, 1, 0, 0, 0, 0, false},
		{"enough traces and a real multiplier", 50, 3, 0, 0, 0, 0, true},
		{"just under the trace count", 49, 3, 0, 0, 0, 0, false},
		{"just under the trace multiplier", 50, 2.9, 0, 0, 0, 0, false},

		// Sustained slowness at volume is a signal on its own, whatever the
		// multiplier says.
		{"slow at volume", 100, 1, 1000, 0, 0, 0, true},
		{"slow but low volume", 99, 1, 5000, 0, 0, 0, false},

		{"log volume spike", 0, 0, 0, 50, 3, 0, true},
		{"log spike below the count", 0, 0, 0, 49, 10, 0, false},

		// Errors are cheap to count and expensive to ignore, so they carry the
		// lowest bar of the four.
		{"error logs alone", 0, 0, 0, 0, 0, 10, true},
		{"one error short", 0, 0, 0, 0, 0, 9, false},

		{"nothing at all", 0, 0, 0, 0, 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isSignificantTrafficSignal(c.traceCount, c.traceMultiplier, c.avgLatency,
				c.logCount, c.logMultiplier, c.errorLogCount)
			if got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

// A rate is per minute. A zero or negative window would divide by zero, and a
// zero count is not "no data", it is a rate of zero.
func TestRate(t *testing.T) {
	if got := rate(120, 2); got != 60 {
		t.Fatalf("120 over 2 min: got %v, want 60", got)
	}
	for _, c := range []struct {
		count   int64
		minutes float64
	}{
		{0, 5}, {-1, 5}, {100, 0}, {100, -5},
	} {
		if got := rate(c.count, c.minutes); got != 0 {
			t.Fatalf("rate(%d, %v): got %v, want 0", c.count, c.minutes, got)
		}
	}
}

// A multiplier against a zero baseline is undefined, not infinite. Returning
// zero is what makes describeTrafficSignal say "no recent baseline" instead of
// printing +Inf at the reader.
func TestRateMultiplierRefusesAnUndefinedComparison(t *testing.T) {
	if got := rateMultiplier(60, 20); got != 3 {
		t.Fatalf("got %v, want 3", got)
	}
	for _, c := range []struct{ current, baseline float64 }{
		{0, 20}, {60, 0}, {-1, 20}, {60, -1},
	} {
		if got := rateMultiplier(c.current, c.baseline); got != 0 {
			t.Fatalf("rateMultiplier(%v, %v): got %v, want 0", c.current, c.baseline, got)
		}
	}
}

// The label is the sentence a human reads. A precise-looking "3.0x" on a
// multiplier that is really a division by almost nothing would overstate what
// is known, which is why the top band is worded rather than numbered.
func TestBaselineLabel(t *testing.T) {
	cases := map[float64]string{
		250: "massive spike vs near-zero usual activity",
		100: "massive spike vs near-zero usual activity",
		42:  "42x usual activity",
		10:  "10x usual activity",
		3.5: "3.5x usual activity",
		3:   "3.0x usual activity",
		2.9: "above usual activity",
		1:   "above usual activity",
	}
	for multiplier, want := range cases {
		if got := baselineLabel(multiplier); got != want {
			t.Fatalf("baselineLabel(%v): got %q, want %q", multiplier, got, want)
		}
	}
}

// The description is written in English on purpose: the api layer localizes it,
// and a French string stored here breaks the cause detection downstream.
func TestDescribeTrafficSignalReportsWhatIsKnown(t *testing.T) {
	withBaseline := describeTrafficSignal(TrafficAnomalySignal{
		Service: "checkout", TraceCount: 900, TraceMultiplier: 40,
	}, 15)
	if !strings.Contains(withBaseline, "checkout received 900 requests/traces in 15 min") {
		t.Fatalf("got %q", withBaseline)
	}
	if !strings.Contains(withBaseline, "40x usual activity") {
		t.Fatalf("the multiplier belongs in the sentence: %q", withBaseline)
	}

	// Without a baseline the sentence has to say so rather than imply a
	// comparison it cannot make.
	noBaseline := describeTrafficSignal(TrafficAnomalySignal{
		Service: "checkout", TraceCount: 900,
	}, 15)
	if !strings.Contains(noBaseline, "no recent baseline") {
		t.Fatalf("got %q, want it to admit there is no baseline", noBaseline)
	}

	// Latency is only worth a clause once it is bad enough to act on.
	slow := describeTrafficSignal(TrafficAnomalySignal{
		Service: "checkout", TraceCount: 900, TraceMultiplier: 40, AvgLatencyMS: 2500,
	}, 15)
	if !strings.Contains(slow, "average latency 2500 ms") {
		t.Fatalf("got %q", slow)
	}
	fast := describeTrafficSignal(TrafficAnomalySignal{
		Service: "checkout", TraceCount: 900, TraceMultiplier: 40, AvgLatencyMS: 120,
	}, 15)
	if strings.Contains(fast, "average latency") {
		t.Fatalf("a healthy latency must not be reported as a symptom: %q", fast)
	}

	// A signal with nothing countable still has to produce a sentence rather
	// than an empty description in the panel.
	empty := describeTrafficSignal(TrafficAnomalySignal{Service: "checkout"}, 15)
	if empty == "" || !strings.Contains(empty, "checkout") {
		t.Fatalf("got %q, want a fallback naming the service", empty)
	}
}

// Rounding is what stops a 3.0000000000000004x multiplier reaching the UI.
func TestRound1(t *testing.T) {
	cases := map[float64]float64{0: 0, 3.04: 3, 3.05: 3.1, 3.44: 3.4, -3.46: -3.5}
	for in, want := range cases {
		if got := round1(in); got != want {
			t.Fatalf("round1(%v): got %v, want %v", in, got, want)
		}
	}
}

func TestContainsHelpers(t *testing.T) {
	names := []string{"api", "worker"}
	if !containsString(names, "api") || containsString(names, "API") || containsString(nil, "api") {
		t.Fatal("containsString must match exactly and tolerate an empty list")
	}

	signals := []TrafficAnomalySignal{{Service: "api"}}
	if !containsSignal(signals, "api") || containsSignal(signals, "worker") || containsSignal(nil, "api") {
		t.Fatal("containsSignal must match on the service name and tolerate an empty list")
	}
}
