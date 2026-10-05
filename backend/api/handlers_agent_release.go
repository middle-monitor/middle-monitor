package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

// agentPlatforms is what build.sh produces.
var agentPlatforms = []struct{ OS, Arch string }{
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
}

// AgentRelease describes the binaries the platform is serving right now, so a
// configuration-management tool can answer "does this host already have the
// right version?" without downloading 14 MB per host per run.
type AgentRelease struct {
	Version   string                 `json:"version"`
	Binaries  map[string]AgentBinary `json:"binaries"`
	Checksums map[string]string      `json:"sha256"`
	URLs      map[string]string      `json:"url"`
}

// AgentBinary is one built artifact.
type AgentBinary struct {
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	UpdatedAt time.Time `json:"updated_at"`
}

// checksumCache keeps the digest of a file until the file changes: hashing a
// 14 MB binary on every request would be wasted work.
var checksumCache sync.Map // path -> checksumEntry

type checksumEntry struct {
	sum     string
	modTime time.Time
	size    int64
}

// agentBinaryPath finds the built binary for one platform, looking in the same
// places the download handler does.
func agentBinaryPath(osName, arch string) (string, bool) {
	name := fmt.Sprintf("middle-monitor-agent-%s-%s", osName, arch)
	candidates := []string{
		filepath.Join("../agent/dist", name),
		filepath.Join("agent/dist", name),
		filepath.Join("./agent/dist", name),
	}
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "../agent/dist", name),
			filepath.Join(exeDir, "agent/dist", name),
		)
	}

	for _, candidate := range candidates {
		absPath, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(absPath); err == nil && !info.IsDir() {
			return absPath, true
		}
	}
	return "", false
}

// agentFileChecksum returns the sha256 of a file, recomputing it only when the
// file has been replaced.
func agentFileChecksum(path string, info os.FileInfo) (string, error) {
	if cached, ok := checksumCache.Load(path); ok {
		entry := cached.(checksumEntry)
		if entry.modTime.Equal(info.ModTime()) && entry.size == info.Size() {
			return entry.sum, nil
		}
	}

	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	sum := hex.EncodeToString(digest.Sum(nil))
	checksumCache.Store(path, checksumEntry{sum: sum, modTime: info.ModTime(), size: info.Size()})
	return sum, nil
}

// agentVersion reads the version stamped into the binaries by build.sh. It is
// written next to them so the served version always matches the served files.
func agentVersion() string {
	for _, platform := range agentPlatforms {
		binary, ok := agentBinaryPath(platform.OS, platform.Arch)
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(filepath.Dir(binary), "VERSION"))
		if err != nil {
			continue
		}
		if version := strings.TrimSpace(string(data)); version != "" {
			return version
		}
	}
	return "dev"
}

// handleAgentLatest publishes the version and the checksums of the binaries on
// offer, plus the versioned URL to pin.
func handleAgentLatest() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version := agentVersion()
		release := AgentRelease{
			Version:   version,
			Binaries:  map[string]AgentBinary{},
			Checksums: map[string]string{},
			URLs:      map[string]string{},
		}

		base := requestBaseURL(r)
		for _, platform := range agentPlatforms {
			key := platform.OS + "/" + platform.Arch
			path, ok := agentBinaryPath(platform.OS, platform.Arch)
			if !ok {
				continue
			}
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			sum, err := agentFileChecksum(path, info)
			if err != nil {
				continue
			}
			release.Binaries[key] = AgentBinary{SHA256: sum, Size: info.Size(), UpdatedAt: info.ModTime().UTC()}
			release.Checksums[key] = sum
			release.URLs[key] = fmt.Sprintf("%s/api/v1/agents/download/%s/%s/%s", base, version, platform.OS, platform.Arch)
		}

		if len(release.Binaries) == 0 {
			respondError(w, http.StatusNotFound, ErrAgentReleaseEmpty)
			return
		}

		w.Header().Set("Cache-Control", "no-cache")
		respondJSON(w, release)
	}
}

// handleAgentChecksum serves the digest of one binary in the shasum format, so
// `sha256sum -c` and Ansible's get_url checksum: can consume it directly.
func handleAgentChecksum() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		osName, arch := vars["os"], vars["arch"]
		if !validAgentPlatform(osName, arch) {
			respondError(w, http.StatusBadRequest, ErrAgentPlatformInvalid)
			return
		}

		path, ok := agentBinaryPath(osName, arch)
		if !ok {
			respondError(w, http.StatusNotFound, ErrAgentBinaryMissing)
			return
		}
		info, err := os.Stat(path)
		if err != nil {
			respondError(w, http.StatusInternalServerError, ErrAgentBinaryRead)
			return
		}
		sum, err := agentFileChecksum(path, info)
		if err != nil {
			respondError(w, http.StatusInternalServerError, ErrAgentBinaryRead)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "%s  middle-monitor-agent-%s-%s\n", sum, osName, arch)
	}
}

func validAgentPlatform(osName, arch string) bool {
	for _, platform := range agentPlatforms {
		if platform.OS == osName && platform.Arch == arch {
			return true
		}
	}
	return false
}

// requestBaseURL is the public URL of this API, as a caller outside should
// write it.
//
// PUBLIC_API_URL wins when set, because the headers cannot be trusted to know:
// behind a Cloudflare tunnel, TLS is terminated at the edge and the origin only
// ever sees plain HTTP, so X-Forwarded-Proto honestly reports "http" and every
// URL this API publishes — including the one baked into each agent's config by
// the install script — comes out as http://.
//
// The headers remain the fallback, so a plain reverse-proxy deployment needs no
// configuration.
func requestBaseURL(r *http.Request) string {
	if configured := strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_API_URL")), "/"); configured != "" {
		return configured
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		// A proxy chain appends, so the first value is the client's protocol.
		scheme = strings.TrimSpace(strings.Split(proto, ",")[0])
	}
	host := r.Host
	if fwdHost := r.Header.Get("X-Forwarded-Host"); fwdHost != "" {
		host = strings.TrimSpace(strings.Split(fwdHost, ",")[0])
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}
