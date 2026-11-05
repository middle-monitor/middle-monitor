package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeCommands(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// The check_nrpe case: fixed flags, one value per flag, and the remote
// arguments as a list that may be empty.
func TestExpandArgs(t *testing.T) {
	template := []string{"-H", "$host$", "-c", "$command$", "-t", "$host$:$port$", "-a", "$args...$"}
	vars := map[string][]string{
		"host": {"10.0.3.12"}, "port": {"5666"}, "command": {"check_disk"}, "args": {"20%", "10%", "/"},
	}
	got, err := expandArgs(template, vars)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	want := []string{"-H", "10.0.3.12", "-c", "check_disk", "-t", "10.0.3.12:5666", "-a", "20%", "10%", "/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A check created without a variable its command needs must not run with an
// empty argument, which some plugins read as "check everything".
func TestMissingVariableIsAnError(t *testing.T) {
	_, err := expandArgs([]string{"-H", "$host$"}, map[string][]string{})
	if !errors.Is(err, errCommandVarMissing) {
		t.Fatalf("err %v, want errCommandVarMissing", err)
	}
	_, err = expandArgs([]string{"$args...$"}, map[string][]string{})
	if !errors.Is(err, errCommandVarMissing) {
		t.Fatalf("list: err %v, want errCommandVarMissing", err)
	}
}

func TestLineBreakInVariableIsRefused(t *testing.T) {
	_, err := expandArgs([]string{"$args...$"}, map[string][]string{"args": {"ok", "a\nb"}})
	if !errors.Is(err, errCommandVarInvalid) {
		t.Fatalf("err %v, want errCommandVarInvalid", err)
	}
}

// Two roles may ship the same command name; the binary behind it must not
// depend on which file Ansible wrote last.
func TestFirstCommandDefinitionWins(t *testing.T) {
	dir := t.TempDir()
	writeCommands(t, dir, "10-base.yaml", "commands:\n  disk:\n    path: /usr/lib/nagios/plugins/check_disk\n")
	writeCommands(t, dir, "20-other.yaml", "commands:\n  disk:\n    path: /tmp/evil\n  load:\n    path: /usr/lib/nagios/plugins/check_load\n")

	commands, err := loadCommands(StringOrList{filepath.Join(dir, "*.yaml")})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if commands["disk"].Path != "/usr/lib/nagios/plugins/check_disk" {
		t.Fatalf("disk runs %q, want the first definition", commands["disk"].Path)
	}
	if _, ok := commands["load"]; !ok {
		t.Fatal("load from the second file is missing")
	}
}

// A relative path would be resolved through PATH or the working directory,
// so the binary that runs would not be the one the operator wrote down.
func TestRelativeCommandPathIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeCommands(t, dir, "bad.yaml", "commands:\n  disk:\n    path: check_disk\n")
	_, err := loadCommands(StringOrList{filepath.Join(dir, "*.yaml")})
	if !errors.Is(err, errCommandPathNotAbsolute) {
		t.Fatalf("err %v, want errCommandPathNotAbsolute", err)
	}
}

func TestCollectorNeedsAZone(t *testing.T) {
	err := PrepareCollector(&CollectorConfig{Enabled: true})
	if !errors.Is(err, errCollectorZoneMissing) {
		t.Fatalf("err %v, want errCollectorZoneMissing", err)
	}
}
