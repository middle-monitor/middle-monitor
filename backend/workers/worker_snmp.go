package workers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"middle-monitor/backend/models"

	"github.com/gosnmp/gosnmp"
)

const defaultSNMPCommunity = "public"

// sysUptimeOID is used as the default OID when none is configured — it is
// universally supported and proves the device is reachable and responding.
const sysUptimeOID = "1.3.6.1.2.1.1.3.0"

func parseSNMPCredentials(credentials *string) (community, oid string) {
	community = defaultSNMPCommunity
	oid = sysUptimeOID

	if credentials == nil || *credentials == "" {
		return
	}
	var creds map[string]string
	if err := json.Unmarshal([]byte(*credentials), &creds); err != nil {
		return
	}
	if c, ok := creds["community"]; ok && c != "" {
		community = c
	}
	if o, ok := creds["oid"]; ok && o != "" {
		oid = o
	}
	return
}

func snmpOIDValue(pdu gosnmp.SnmpPDU) (numeric float64, isNumeric bool, display string) {
	switch pdu.Type {
	case gosnmp.Integer, gosnmp.Counter32, gosnmp.Gauge32, gosnmp.TimeTicks,
		gosnmp.Counter64, gosnmp.Uinteger32:
		v := gosnmp.ToBigInt(pdu.Value)
		f := float64(v.Int64())
		return f, true, fmt.Sprintf("%g", f)
	case gosnmp.OctetString:
		b, ok := pdu.Value.([]byte)
		if ok {
			return 0, false, string(b)
		}
		return 0, false, fmt.Sprintf("%v", pdu.Value)
	default:
		return 0, false, fmt.Sprintf("%v", pdu.Value)
	}
}

func executeSNMPService(service models.Service) models.ServiceResult {
	result := models.ServiceResult{
		ServiceID: service.ID,
		Timestamp: time.Now().UTC(),
	}

	community, oid := parseSNMPCredentials(service.Credentials)

	host := service.Host
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimPrefix(host, "https://")
	if idx := strings.Index(host, "/"); idx != -1 {
		host = host[:idx]
	}
	// Strip port if embedded — gosnmp sets port separately
	target := host
	port := uint16(161)
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		portStr := host[idx+1:]
		var p uint16
		if _, err := fmt.Sscanf(portStr, "%d", &p); err == nil {
			target = host[:idx]
			port = p
		}
	}

	g := &gosnmp.GoSNMP{
		Target:    target,
		Port:      port,
		Community: community,
		Version:   gosnmp.Version2c,
		Timeout:   5 * time.Second,
		Retries:   1,
	}

	start := time.Now()
	if err := g.Connect(); err != nil {
		result.Status = "failure"
		msg := fmt.Sprintf("SNMP connect failed: %v", err)
		result.Message = &msg
		return result
	}
	defer g.Conn.Close()

	pdus, err := g.Get([]string{oid})
	latency := float64(time.Since(start).Milliseconds())
	result.Latency = &latency

	if err != nil {
		result.Status = "failure"
		msg := fmt.Sprintf("SNMP GET failed: %v", err)
		result.Message = &msg
		return result
	}

	if len(pdus.Variables) == 0 || pdus.Variables[0].Type == gosnmp.NoSuchObject || pdus.Variables[0].Type == gosnmp.NoSuchInstance {
		result.Status = "failure"
		msg := fmt.Sprintf("OID %s not found on device", oid)
		result.Message = &msg
		return result
	}

	pdu := pdus.Variables[0]
	numericVal, isNumeric, display := snmpOIDValue(pdu)

	// Apply thresholds only when the OID returns a numeric value
	if isNumeric {
		criticalVal := service.CriticalThreshold
		if criticalVal == nil {
			criticalVal = service.FailureThreshold // backward compat
		}
		if criticalVal != nil && numericVal > *criticalVal {
			result.Status = "failure"
			msg := fmt.Sprintf("SNMP OID %s = %s (exceeds critical threshold %.0f)", oid, display, *criticalVal)
			result.Message = &msg
			return result
		}
		if service.WarningThreshold != nil && numericVal > *service.WarningThreshold {
			result.Status = "warning"
			msg := fmt.Sprintf("SNMP OID %s = %s (exceeds warning threshold %.0f)", oid, display, *service.WarningThreshold)
			result.Message = &msg
			return result
		}
	}

	result.Status = "success"
	msg := fmt.Sprintf("SNMP OID %s = %s", oid, display)
	result.Message = &msg
	return result
}
