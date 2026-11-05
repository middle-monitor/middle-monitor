package services

import (
	"errors"
	"reflect"
	"testing"
)

func strPtr(s string) *string { return &s }

func TestParseCommandCheckConfig(t *testing.T) {
	cfg, err := ParseCommandCheckConfig(strPtr(`{"zone":"paris-dc1","command":"nrpe","vars":{"command":["check_disk"],"args":["20%","10%","/"]}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Zone != "paris-dc1" || cfg.Command != "nrpe" || len(cfg.Vars["args"]) != 3 {
		t.Fatalf("got %+v", cfg)
	}
}

// Every refusal here is a check that would otherwise never run: the collector
// rejects the same names, so the API must say so at creation time.
func TestParseCommandCheckConfigRefusals(t *testing.T) {
	cases := map[string]struct {
		raw  *string
		want error
	}{
		"missing":        {nil, ErrCommandConfigInvalid},
		"not json":       {strPtr("check_disk"), ErrCommandConfigInvalid},
		"no zone":        {strPtr(`{"command":"nrpe"}`), ErrCommandZoneInvalid},
		"zone with path": {strPtr(`{"zone":"../a","command":"nrpe"}`), ErrCommandZoneInvalid},
		"no command":     {strPtr(`{"zone":"z"}`), ErrCommandNameInvalid},
		"shell command":  {strPtr(`{"zone":"z","command":"check_disk -w 10"}`), ErrCommandNameInvalid},
		"bad var name":   {strPtr(`{"zone":"z","command":"nrpe","vars":{"a-b":["x"]}}`), ErrCommandVarInvalid},
		"line break":     {strPtr(`{"zone":"z","command":"nrpe","vars":{"args":["a\nb"]}}`), ErrCommandVarInvalid},
	}
	for name, tc := range cases {
		if _, err := ParseCommandCheckConfig(tc.raw); !errors.Is(err, tc.want) {
			t.Errorf("%s: err %v, want %v", name, err, tc.want)
		}
	}
}

// check_nrpe -H $host$ is the common case: the check's host fills it, unless
// the check sets its own host variable (an NRPE proxy, for instance).
func TestCommandVarsDefaultsHostToTheCheckHost(t *testing.T) {
	got := CommandVars(CommandCheckConfig{Vars: map[string][]string{"command": {"check_load"}}}, "10.0.3.12")
	want := map[string][]string{"command": {"check_load"}, "host": {"10.0.3.12"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	got = CommandVars(CommandCheckConfig{Vars: map[string][]string{"host": {"proxy"}}}, "10.0.3.12")
	if got["host"][0] != "proxy" {
		t.Fatalf("host %v, want the check's own variable", got["host"])
	}
}
