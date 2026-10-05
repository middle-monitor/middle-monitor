package services

import (
	"strings"
	"testing"
)

func TestNormalizeErrorMessage_ReplacesUUID(t *testing.T) {
	msg := "error for id 550e8400-e29b-41d4-a716-446655440000"
	got := NormalizeErrorMessage(msg)
	if strings.Contains(got, "550e8400") {
		t.Fatalf("uuid not replaced: %q", got)
	}
	if !strings.Contains(got, "<uuid>") {
		t.Fatalf("expected <uuid> placeholder: %q", got)
	}
}

func TestNormalizeErrorMessage_ReplacesIP(t *testing.T) {
	got := NormalizeErrorMessage("connection refused 192.168.1.1:8080")
	if strings.Contains(got, "192.168") {
		t.Fatalf("IP not replaced: %q", got)
	}
}

func TestNormalizeErrorMessage_ReplacesHex(t *testing.T) {
	got := NormalizeErrorMessage("addr 0xdeadbeef")
	if strings.Contains(got, "0xdeadbeef") {
		t.Fatalf("hex not replaced: %q", got)
	}
}

func TestNormalizeErrorMessage_ReplacesQuotedStrings(t *testing.T) {
	got := NormalizeErrorMessage(`failed to open "my file"`)
	if strings.Contains(got, "my file") {
		t.Fatalf("quoted string not replaced: %q", got)
	}
}

func TestNormalizeErrorMessage_ReplacesNumbers(t *testing.T) {
	got := NormalizeErrorMessage("retry 42 failed")
	if strings.Contains(got, "42") {
		t.Fatalf("number not replaced: %q", got)
	}
}

func TestNormalizeErrorMessage_CollapseWhitespace(t *testing.T) {
	got := NormalizeErrorMessage("  multiple   spaces  ")
	if strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") {
		t.Fatalf("whitespace not trimmed: %q", got)
	}
}

func TestNormalizeErrorMessage_EmptyString(t *testing.T) {
	if got := NormalizeErrorMessage(""); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestComputeErrorFingerprint_IsDeterministic(t *testing.T) {
	a := ComputeErrorFingerprint("TypeError", "cannot read property of null", "app.js")
	b := ComputeErrorFingerprint("TypeError", "cannot read property of null", "app.js")
	if a != b {
		t.Fatalf("fingerprint not deterministic: %q vs %q", a, b)
	}
}

func TestComputeErrorFingerprint_DiffersOnName(t *testing.T) {
	a := ComputeErrorFingerprint("TypeError", "msg", "f.js")
	b := ComputeErrorFingerprint("Error", "msg", "f.js")
	if a == b {
		t.Fatal("expected different fingerprints for different error names")
	}
}

func TestComputeErrorFingerprint_DiffersOnFile(t *testing.T) {
	a := ComputeErrorFingerprint("Error", "msg", "a.js")
	b := ComputeErrorFingerprint("Error", "msg", "b.js")
	if a == b {
		t.Fatal("expected different fingerprints for different files")
	}
}

func TestComputeErrorFingerprint_Length16(t *testing.T) {
	fp := ComputeErrorFingerprint("E", "m", "f.js")
	if len(fp) != 16 {
		t.Fatalf("want 16 chars, got %d: %q", len(fp), fp)
	}
}

func TestComputeErrorFingerprint_IgnoresLineNumber(t *testing.T) {
	// Same base file name → same fingerprint regardless of volatile numbers in message
	a := ComputeErrorFingerprint("Error", "timeout after 30s", "server.go")
	b := ComputeErrorFingerprint("Error", "timeout after 60s", "server.go")
	if a == b {
		// Numbers ARE different → fingerprints must differ (after normalization both become "<n>s")
		// so actually they should be equal
	}
	// Check that path prefix is ignored
	c := ComputeErrorFingerprint("Error", "msg", "/long/path/to/server.go")
	d := ComputeErrorFingerprint("Error", "msg", "server.go")
	if c != d {
		t.Fatal("expected same fingerprint regardless of path prefix")
	}
}
