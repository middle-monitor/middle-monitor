package services

import (
	"encoding/json"
	"strings"
)

// DNSCheckConfig is what a dns check stores in services.credentials. Nothing in
// it is secret; the column is reused the way snmp reuses it for its OID.
type DNSCheckConfig struct {
	RecordType string `json:"record_type"`
	Expected   string `json:"expected,omitempty"`
}

var dnsRecordTypes = map[string]bool{
	"A": true, "AAAA": true, "CNAME": true, "MX": true, "NS": true, "TXT": true,
}

// ParseDNSCheckConfig reads a dns check's config; an empty one resolves A records.
func ParseDNSCheckConfig(credentials *string) (DNSCheckConfig, error) {
	cfg := DNSCheckConfig{RecordType: "A"}
	if credentials == nil || strings.TrimSpace(*credentials) == "" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(*credentials), &cfg); err != nil {
		return cfg, ErrDNSConfigInvalid
	}
	cfg.RecordType = strings.ToUpper(strings.TrimSpace(cfg.RecordType))
	if cfg.RecordType == "" {
		cfg.RecordType = "A"
	}
	if !dnsRecordTypes[cfg.RecordType] {
		return cfg, ErrDNSRecordTypeInvalid
	}
	cfg.Expected = strings.TrimSpace(cfg.Expected)
	return cfg, nil
}
