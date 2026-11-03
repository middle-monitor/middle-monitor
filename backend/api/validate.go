package api

import (
	"net"
	"strconv"
	"strings"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

const (
	maxNameLen         = 255
	maxHostLen         = 500
	minServiceInterval = 60
	maxServiceInterval = 3600
	minMaxAttempts     = 1
	maxMaxAttempts     = 10
)

var validServiceTypes = map[string]bool{
	"http":        true,
	"ping":        true,
	"tcp":         true,
	"dns":         true,
	"snmp":        true,
	"certificate": true,
	"sql":         true,
}

var validChannelTypes = map[string]bool{
	"email":    true,
	"slack":    true,
	"jsm":      true,
	"whatsapp": true,
	"webhook":  true,
}

var validMaintenanceTargetTypes = map[string]bool{
	"service": true,
	"host":    true,
}

func validateName(field, value string, maxLen int) error {
	v := strings.TrimSpace(value)
	if v == "" {
		return &FieldRequiredError{Field: field}
	}
	if len(v) > maxLen {
		return &FieldTooLongError{Field: field, Max: maxLen}
	}
	return nil
}

func validateServiceType(t string) error {
	if validServiceTypes[t] {
		return nil
	}
	// Allow prefixed types created by internal systems.
	if strings.HasPrefix(t, "error_service_") || strings.HasPrefix(t, "agent_") {
		return nil
	}
	return ErrServiceTypeInvalid
}

func validateServiceInterval(v int) error {
	if v < minServiceInterval || v > maxServiceInterval {
		return &RangeError{What: "service interval", Min: minServiceInterval, Max: maxServiceInterval, Unit: "seconds"}
	}
	return nil
}

func validateMaxAttempts(v int) error {
	if v < minMaxAttempts || v > maxMaxAttempts {
		return &RangeError{What: "max attempts", Min: minMaxAttempts, Max: maxMaxAttempts}
	}
	return nil
}

func validateExpectedStatusCode(v int) error {
	// An HTTP status code is exactly 3 digits (RFC 7230), so 100-999. We don't
	// restrict to well-known codes: some servers return custom codes (e.g. 743).
	if v < 100 || v > 999 {
		return ErrExpectedStatusCodeInvalid
	}
	return nil
}

// validateTCPTarget requires host:port, the only form a tcp check can dial.
func validateTCPTarget(target string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(target))
	if err != nil || host == "" {
		return ErrTCPTargetInvalid
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return ErrTCPTargetInvalid
	}
	return nil
}

func validateCheckConfig(service models.Service) error {
	switch service.Type {
	case "tcp":
		return validateTCPTarget(service.Host)
	case "dns":
		_, err := services.ParseDNSCheckConfig(service.Credentials)
		return err
	}
	return nil
}

func validateChannelType(t string) error {
	if !validChannelTypes[t] {
		return ErrChannelTypeInvalid
	}
	return nil
}

func validateMaintenanceTargetType(t string) error {
	if !validMaintenanceTargetTypes[t] {
		return ErrTargetTypeInvalid
	}
	return nil
}
