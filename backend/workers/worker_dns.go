package workers

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

const dnsLookupTimeout = 10 * time.Second

// executeDNSService resolves the check's name. With an expected value, one of
// the answers must match it: that is what catches a hijacked or edited record.
func executeDNSService(service models.Service) models.ServiceResult {
	result := models.ServiceResult{
		ServiceID: service.ID,
		Timestamp: time.Now().UTC(),
	}
	fail := func(msg string) models.ServiceResult {
		result.Status = "failure"
		result.Message = &msg
		return result
	}

	cfg, err := services.ParseDNSCheckConfig(service.Credentials)
	if err != nil {
		return fail(fmt.Sprintf("Invalid DNS check config: %v", err))
	}
	name := strings.TrimSpace(service.Host)

	ctx, cancel := context.WithTimeout(context.Background(), dnsLookupTimeout)
	defer cancel()
	start := time.Now()
	answers, err := lookupDNS(ctx, net.DefaultResolver, cfg.RecordType, name)
	latency := elapsedMs(start)
	if err != nil {
		return fail(fmt.Sprintf("DNS %s lookup for %s failed: %v", cfg.RecordType, name, err))
	}
	if len(answers) == 0 {
		return fail(fmt.Sprintf("DNS %s lookup for %s returned no record", cfg.RecordType, name))
	}
	if cfg.Expected != "" && !dnsAnswersContain(answers, cfg.Expected) {
		return fail(fmt.Sprintf("DNS %s %s resolved to %s, expected %s", cfg.RecordType, name, strings.Join(answers, ", "), cfg.Expected))
	}

	okMsg := fmt.Sprintf("DNS %s %s resolved to %s", cfg.RecordType, name, strings.Join(answers, ", "))
	applyLatencyThresholds(&result, service, "DNS lookup", latency, okMsg)
	return result
}

func lookupDNS(ctx context.Context, r *net.Resolver, recordType, name string) ([]string, error) {
	var answers []string
	switch recordType {
	case "A", "AAAA":
		network := "ip4"
		if recordType == "AAAA" {
			network = "ip6"
		}
		ips, err := r.LookupIP(ctx, network, name)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			answers = append(answers, ip.String())
		}
	case "CNAME":
		// Returns the name itself when it has no CNAME but still resolves.
		cname, err := r.LookupCNAME(ctx, name)
		if err != nil {
			return nil, err
		}
		answers = append(answers, cname)
	case "MX":
		records, err := r.LookupMX(ctx, name)
		if err != nil {
			return nil, err
		}
		for _, mx := range records {
			answers = append(answers, mx.Host)
		}
	case "NS":
		records, err := r.LookupNS(ctx, name)
		if err != nil {
			return nil, err
		}
		for _, ns := range records {
			answers = append(answers, ns.Host)
		}
	case "TXT":
		records, err := r.LookupTXT(ctx, name)
		if err != nil {
			return nil, err
		}
		answers = records
	default:
		return nil, services.ErrDNSRecordTypeInvalid
	}
	return answers, nil
}

// dnsAnswersContain ignores case and the trailing dot of fully qualified names.
func dnsAnswersContain(answers []string, expected string) bool {
	want := normalizeDNSAnswer(expected)
	for _, a := range answers {
		if normalizeDNSAnswer(a) == want {
			return true
		}
	}
	return false
}

func normalizeDNSAnswer(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}
