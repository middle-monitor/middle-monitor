package services

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
)

// Volatile token patterns stripped before hashing so that two occurrences of
// the "same" error (differing only by ids, timestamps, addresses, numbers...)
// collapse to a single fingerprint.
var (
	reFPUUID   = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	reFPHex    = regexp.MustCompile(`(?i)0x[0-9a-f]+|\b[0-9a-f]{12,}\b`)
	reFPQuoted = regexp.MustCompile(`'[^']*'|"[^"]*"`)
	reFPIP     = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`)
	reFPNum    = regexp.MustCompile(`\d+`)
	reFPSpace  = regexp.MustCompile(`\s+`)
)

// NormalizeErrorMessage replaces volatile tokens with stable placeholders so
// that distinct occurrences of the same logical error normalize identically.
func NormalizeErrorMessage(msg string) string {
	s := strings.ToLower(msg)
	s = reFPUUID.ReplaceAllString(s, "<uuid>")
	s = reFPIP.ReplaceAllString(s, "<ip>")
	s = reFPHex.ReplaceAllString(s, "<hex>")
	s = reFPQuoted.ReplaceAllString(s, "<str>")
	s = reFPNum.ReplaceAllString(s, "<n>")
	s = reFPSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// ComputeErrorFingerprint returns a short, stable hash grouping occurrences of
// the same error. It is deliberately based on (name, normalized message, base
// filename) and ignores line numbers, which often drift between releases.
func ComputeErrorFingerprint(name, message, file string) string {
	base := strings.ToLower(strings.TrimSpace(name)) + "|" +
		NormalizeErrorMessage(message) + "|" +
		strings.ToLower(filepath.Base(strings.TrimSpace(file)))
	sum := sha1.Sum([]byte(base))
	return hex.EncodeToString(sum[:])[:16]
}
