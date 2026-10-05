package workers

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"middle-monitor/backend/models"
)

// pingRTTRe captures the real ICMP round-trip from ping output, e.g. "time=8.12 ms".
var pingRTTRe = regexp.MustCompile(`time[=<]\s*([0-9.]+)\s*ms`)

// parsePingRTT extracts the true network RTT (ms) from ping output, excluding the
// subprocess fork/exec overhead that wall-clock timing would include.
func parsePingRTT(output string) (float64, bool) {
	m := pingRTTRe.FindStringSubmatch(output)
	if len(m) < 2 {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func executePingService(service models.Service) models.ServiceResult {
	result := models.ServiceResult{
		ServiceID: service.ID,
		Timestamp: time.Now().UTC(),
	}

	host := service.Host
	// Remove http:// or https:// if user accidentally added it
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	// Remove any path
	if idx := strings.Index(host, "/"); idx != -1 {
		host = host[:idx]
	}

	start := time.Now()

	// ping's own -W covers the echo reply only, not name resolution: without an
	// outer deadline a slow resolver hangs the check past the retry window.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// -W is milliseconds on darwin/windows, seconds on linux.
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "ping", "-n", "1", "-w", "5000", host)
	case "darwin":
		cmd = exec.CommandContext(ctx, "ping", "-c", "1", "-W", "5000", host)
	default:
		cmd = exec.CommandContext(ctx, "ping", "-c", "1", "-W", "5", host)
	}

	out, err := cmd.Output()
	// Prefer the true ICMP RTT from ping output; fall back to wall-clock (which
	// includes ~10ms of subprocess overhead) only if parsing fails.
	latency := float64(time.Since(start).Milliseconds())
	if rtt, ok := parsePingRTT(string(out)); ok {
		latency = rtt
	}

	if err != nil {
		result.Status = "failure"
		msg := fmt.Sprintf("Ping failed: %v", err)
		result.Message = &msg
	} else {
		criticalMs := service.CriticalThreshold
		if criticalMs == nil {
			criticalMs = service.FailureThreshold // backward compat
		}
		if criticalMs != nil && latency > *criticalMs {
			result.Status = "failure"
			msg := fmt.Sprintf("Ping latency (%.0fms) exceeded critical threshold (%.0fms)", latency, *criticalMs)
			result.Message = &msg
			result.Latency = &latency
		} else if service.WarningThreshold != nil && latency > *service.WarningThreshold {
			result.Status = "warning"
			msg := fmt.Sprintf("Ping latency (%.0fms) exceeded warning threshold (%.0fms)", latency, *service.WarningThreshold)
			result.Message = &msg
			result.Latency = &latency
		} else {
			result.Status = "success"
			msg := "Ping successful"
			result.Message = &msg
			result.Latency = &latency
		}
	}

	return result
}
