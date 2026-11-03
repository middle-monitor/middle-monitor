package workers

import (
	"fmt"
	"net"
	"strings"
	"time"

	"middle-monitor/backend/models"
)

const tcpDialTimeout = 10 * time.Second

// executeTCPService opens a connection to host:port and closes it; the latency
// is the connect time, name resolution included.
func executeTCPService(service models.Service) models.ServiceResult {
	result := models.ServiceResult{
		ServiceID: service.ID,
		Timestamp: time.Now().UTC(),
	}
	target := strings.TrimSpace(service.Host)

	start := time.Now()
	conn, err := net.DialTimeout("tcp", target, tcpDialTimeout)
	latency := elapsedMs(start)
	if err != nil {
		result.Status = "failure"
		msg := fmt.Sprintf("TCP connection to %s failed: %v", target, err)
		result.Message = &msg
		return result
	}
	conn.Close()

	applyLatencyThresholds(&result, service, "TCP connect", latency, fmt.Sprintf("TCP port open on %s", target))
	return result
}

// elapsedMs keeps sub-millisecond precision: a connect on a LAN would read 0ms otherwise.
func elapsedMs(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}

// applyLatencyThresholds classifies a check that answered, the way ping does:
// above critical fails, above warning warns.
func applyLatencyThresholds(result *models.ServiceResult, service models.Service, label string, latency float64, okMsg string) {
	result.Latency = &latency
	critical := service.CriticalThreshold
	if critical == nil {
		critical = service.FailureThreshold
	}
	var msg string
	switch {
	case critical != nil && latency > *critical:
		result.Status = "failure"
		msg = fmt.Sprintf("%s latency (%.0fms) exceeded critical threshold (%.0fms)", label, latency, *critical)
	case service.WarningThreshold != nil && latency > *service.WarningThreshold:
		result.Status = "warning"
		msg = fmt.Sprintf("%s latency (%.0fms) exceeded warning threshold (%.0fms)", label, latency, *service.WarningThreshold)
	default:
		result.Status = "success"
		msg = okMsg
	}
	result.Message = &msg
}
