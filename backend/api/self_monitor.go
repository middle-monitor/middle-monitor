package api

import (
	"database/sql"
	"log/slog"
	"os"
	"strings"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

// selfMonitorHostName is the host the seeded checks are attached to, so they
// appear under it instead of floating host-less on the services page.
const selfMonitorHostName = "middle-monitor"

// ensureSelfMonitorChecks seeds uptime checks for Middle-Monitor's own public
// endpoints into the self-monitoring org (SELF_MONITOR_ORG_SLUG, the same org
// the middle-front / middle-back SDK tokens report into). Opt-in: a no-op
// when the slug is unset. Idempotent: checks are keyed by name and never
// modified once created, so operator edits in the UI survive restarts.
//
// Targets come from FRONTEND_URL (the public dashboard origin) and
// PUBLIC_API_URL (the public API origin, e.g. https://api.middlemonitor.io);
// each contributes an HTTP check plus a certificate-expiry check, and the API
// origin also gets a receiver+DB probe through /api/v1/health/sql.
func ensureSelfMonitorChecks(db *sql.DB) {
	slug := strings.TrimSpace(os.Getenv("SELF_MONITOR_ORG_SLUG"))
	if slug == "" {
		return
	}

	var orgID int64
	if err := db.QueryRow(`SELECT id FROM organizations WHERE slug = $1`, slug).Scan(&orgID); err != nil {
		slog.Warn("org not found, skipping check seed", "slug", slug, "error", err)
		return
	}

	// The host must belong to the same org: a host_id from another org makes the
	// checks invisible on the host page (which filters by org) while still
	// counting toward that host's service count.
	var hostID *int64
	var foundHostID int64
	if err := db.QueryRow(`SELECT id FROM hosts WHERE organization_id = $1 AND name = $2`, orgID, selfMonitorHostName).Scan(&foundHostID); err == nil {
		hostID = &foundHostID
	} else {
		slog.Warn("host not found, seeding checks without a host", "host", selfMonitorHostName, "slug", slug, "error", err)
	}

	type target struct {
		name string
		// label names the component on the public status page; name stays the
		// technical identifier the rest of the product keys on.
		label string
		typ   string
		host  string
		path  string
		// Whether the check appears on the public status page. Certificate expiry
		// is an operational concern we want alerts on, but it says nothing about
		// whether the service is up, so it stays internal.
		public bool
	}

	var targets []target
	if host := bareHost(os.Getenv("FRONTEND_URL")); host != "" {
		targets = append(targets,
			target{name: "self-frontend", label: "Dashboard", typ: "http", host: host, public: true},
			target{name: "self-cert-frontend", label: "Dashboard TLS certificate", typ: "certificate", host: host},
		)
	}
	if host := bareHost(os.Getenv("PUBLIC_API_URL")); host != "" {
		targets = append(targets,
			target{name: "self-api", label: "API", typ: "http", host: host, path: "/readyz", public: true},
			target{name: "self-receiver-db", label: "Ingestion pipeline", typ: "http", host: host, path: "/api/v1/health/sql", public: true},
			target{name: "self-cert-api", label: "API TLS certificate", typ: "certificate", host: host},
		)
	}
	if len(targets) == 0 {
		slog.Info("no FRONTEND_URL or PUBLIC_API_URL configured, nothing to seed")
		return
	}

	serviceService := services.NewServiceService(db)
	for _, t := range targets {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM services WHERE organization_id = $1 AND name = $2)`, orgID, t.name).Scan(&exists); err != nil {
			slog.Error("check lookup failed", "check", t.name, "error", err)
			continue
		}
		if exists {
			continue
		}

		label := t.label
		svc := models.Service{
			OrganizationID:  orgID,
			HostID:          hostID,
			Name:            t.name,
			DisplayName:     &label,
			Type:            t.typ,
			Host:            t.host,
			Service:         "middle-monitor",
			ServiceInterval: 60,
			MaxAttempts:     3,
			Public:          t.public,
		}
		if t.path != "" {
			path := t.path
			svc.Path = &path
		}
		if _, err := serviceService.CreateService(svc); err != nil {
			// The lookup above is not atomic: two instances booting at once (a
			// rolling update runs the new task before stopping the old one) both
			// reach here and the unique index rejects the loser. That is the
			// index doing its job, not a failure worth an error line.
			if seededByAnotherInstance(db, orgID, t.name) {
				slog.Info("check already created by another instance", "check", t.name)
				continue
			}
			slog.Error("failed to create check", "check", t.name, "error", err)
			continue
		}
		slog.Info("check created", "check", t.name, "type", t.typ, "host", t.host, "path", t.path, "slug", slug)
	}
}

// seededByAnotherInstance reports whether the check exists after our own insert
// failed, which is what a lost race looks like from here.
func seededByAnotherInstance(db *sql.DB, orgID int64, name string) bool {
	var exists bool
	if err := db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM services WHERE organization_id = $1 AND name = $2)`,
		orgID, name).Scan(&exists); err != nil {
		return false
	}
	return exists
}

// bareHost strips the scheme and any path from a URL-ish env value; check
// hosts are stored bare (the http worker tries https first, the certificate
// worker appends :443).
func bareHost(raw string) string {
	raw = strings.TrimSpace(raw)
	// FRONTEND_URL may be a comma-separated CORS list; the first entry is the
	// canonical public origin.
	if i := strings.Index(raw, ","); i >= 0 {
		raw = raw[:i]
	}
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if i := strings.Index(raw, "/"); i >= 0 {
		raw = raw[:i]
	}
	return raw
}
