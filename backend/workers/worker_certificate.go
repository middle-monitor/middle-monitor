package workers

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"middle-monitor/backend/models"
)

func executeCertificateService(service models.Service) models.ServiceResult {
	result := models.ServiceResult{
		ServiceID: service.ID,
		Timestamp: time.Now().UTC(),
	}

	// Clean host - remove protocol if present
	host := service.Host
	if len(host) > 7 && host[:7] == "http://" {
		host = host[7:]
	} else if len(host) > 8 && host[:8] == "https://" {
		host = host[8:]
	}

	// Add port 443 only if no port is already present
	address := host
	if !strings.Contains(host, ":") {
		address = host + ":443"
	}

	// Bound the dial: an unbounded one blocks on the kernel SYN retries past the
	// check interval, which starves the retries meant to absorb a single flap.
	// The dialer timeout covers the TLS handshake too, and is split across the
	// resolved addresses, so a dead anycast IP falls through to the next one.
	dialer := &net.Dialer{Timeout: 5 * time.Second}

	// Check certificate validity
	conn, err := tls.DialWithDialer(dialer, "tcp", address, &tls.Config{
		InsecureSkipVerify: true, // We want to check the cert, not verify it
	})
	if err != nil {
		result.Status = "failure"
		msg := fmt.Sprintf("Certificate check error: %v", err)
		result.Message = &msg
		return result
	}
	defer conn.Close()

	// Check certificate expiration
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		result.Status = "failure"
		msg := "No certificate found"
		result.Message = &msg
		return result
	}

	cert := state.PeerCertificates[0]
	now := time.Now()
	expiresIn := cert.NotAfter.Sub(now)

	// Store expiration timestamp in metadata as JSON for UI to format
	metadata := map[string]interface{}{
		"expires_at": cert.NotAfter.UTC().Format(time.RFC3339),
	}
	metadataJSON, _ := json.Marshal(metadata)
	metadataStr := string(metadataJSON)
	result.Metadata = &metadataStr

	criticalDays := 7
	if service.CriticalThreshold != nil && *service.CriticalThreshold > 0 {
		criticalDays = int(*service.CriticalThreshold)
	}
	warningDays := 30
	if service.WarningThreshold != nil && *service.WarningThreshold > 0 {
		warningDays = int(*service.WarningThreshold)
	}

	if expiresIn < 0 {
		result.Status = "failure"
		msg := "Certificate expired"
		result.Message = &msg
	} else if expiresIn < time.Duration(criticalDays)*24*time.Hour {
		result.Status = "failure"
		msg := fmt.Sprintf("Certificate expires in less than %d days", criticalDays)
		result.Message = &msg
	} else if warningDays > criticalDays && expiresIn < time.Duration(warningDays)*24*time.Hour {
		result.Status = "warning"
		msg := fmt.Sprintf("Certificate expires in less than %d days", warningDays)
		result.Message = &msg
	} else {
		result.Status = "success"
		msg := "Certificate valid"
		result.Message = &msg
	}

	return result
}
