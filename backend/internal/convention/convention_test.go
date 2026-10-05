package convention

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// backendRoot is the module root, two levels above this package.
const backendRoot = "../.."

var (
	inlineErrorsNew = regexp.MustCompile(`errors\.New\("`)
	inlineErrorf    = regexp.MustCompile(`fmt\.Errorf\("[^"]*"`)
	wrapping        = regexp.MustCompile(`fmt\.Errorf\("[^"]*%w`)
	stdLog          = regexp.MustCompile(`\blog\.(Printf|Println|Print|Fatalf|Fatal)\(`)
	sentinelDecl    = regexp.MustCompile(`(?m)^\s*(Err\w+)\s+=`)
)

// sourceFiles returns every non-test .go file in the module, with its path
// relative to the module root.
func sourceFiles(t *testing.T) []string {
	t.Helper()
	return moduleFiles(t, func(path string) bool {
		return strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
	})
}

// markdownFiles returns every .md file in the module, with its path relative to
// the module root.
func markdownFiles(t *testing.T) []string {
	t.Helper()
	return moduleFiles(t, func(path string) bool { return strings.HasSuffix(path, ".md") })
}

func moduleFiles(t *testing.T, keep func(path string) bool) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(backendRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// CI points GOMODCACHE at $CI_PROJECT_DIR/.gomodcache, so a walk that
			// keeps dot directories checks the dependencies instead of the module.
			// The root itself is named ".." and has to survive that rule.
			name := info.Name()
			if path == backendRoot {
				return nil
			}
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !keep(path) {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no source file found, the walk root is wrong")
	}
	return out
}

// Errors have to be reusable values, or a caller can only match them by
// comparing message text. That is not hypothetical here: the API layer used to
// gate its 404s on err.Error() == "service not found", and one such comparison
// had already drifted out of sync with the message it was matching, so a
// missing agent service answered 500 instead of 404. This test is the guard,
// across every package rather than just the handlers.
func TestNoInlineErrors(t *testing.T) {
	var offenders []string
	for _, path := range sourceFiles(t) {
		if filepath.Base(path) == "errors.go" {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for i, line := range strings.Split(string(source), "\n") {
			bad := inlineErrorsNew.MatchString(line) ||
				(inlineErrorf.MatchString(line) && !wrapping.MatchString(line))
			if bad {
				offenders = append(offenders, formatOffender(path, i, line))
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("%d inline errors; declare them in the package's errors.go as a sentinel or a typed error:\n%s",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

// Logs are structured key-value pairs, so they can be queried rather than
// grepped. The standard logger cannot produce them, so its use is the signal.
func TestNoStandardLibraryLogging(t *testing.T) {
	var offenders []string
	for _, path := range sourceFiles(t) {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for i, line := range strings.Split(string(source), "\n") {
			if stdLog.MatchString(line) {
				offenders = append(offenders, formatOffender(path, i, line))
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("%d standard-library log calls; use log/slog with key-value pairs:\n%s",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

// Emoji in a log line survives into the log pipeline, where it is noise no
// query can use. Documentation is covered too: the README described a log line
// by its emoji, which outlived the line itself.
func TestNoEmojiInSource(t *testing.T) {
	var offenders []string
	for _, path := range append(sourceFiles(t), markdownFiles(t)...) {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for i, line := range strings.Split(string(source), "\n") {
			for _, r := range line {
				if r >= 0x1F300 || (r >= 0x2600 && r <= 0x27BF) {
					offenders = append(offenders, formatOffender(path, i, line))
					break
				}
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("%d lines contain an emoji:\n%s", len(offenders), strings.Join(offenders, "\n"))
	}
}

// A sentinel nobody uses is dead weight, and usually the leftover of a message
// that was reworded somewhere else.
func TestEverySentinelIsUsed(t *testing.T) {
	files := sourceFiles(t)

	var body strings.Builder
	for _, path := range files {
		if filepath.Base(path) == "errors.go" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		body.Write(data)
	}
	haystack := body.String()

	var unused []string
	for _, path := range files {
		if filepath.Base(path) != "errors.go" {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, match := range sentinelDecl.FindAllStringSubmatch(string(source), -1) {
			if !strings.Contains(haystack, match[1]) {
				unused = append(unused, path+": "+match[1])
			}
		}
	}
	if len(unused) > 0 {
		t.Fatalf("sentinels declared but never used:\n%s", strings.Join(unused, "\n"))
	}
}

func formatOffender(path string, index int, line string) string {
	return strings.TrimPrefix(path, backendRoot+"/") + ":" +
		strconv.Itoa(index+1) + ": " + strings.TrimSpace(line)
}
