package services

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
)

// frontendBaseURL returns the first configured FRONTEND_URL (the list is also
// used for CORS), normalized without a trailing slash. Empty when unset.
func frontendBaseURL() string {
	raw := os.Getenv("FRONTEND_URL")
	if raw == "" {
		return ""
	}
	first := strings.Split(raw, ",")[0]
	return strings.TrimRight(strings.TrimSpace(first), "/")
}

// ServiceAlertContext appends a human-readable "where" block to an alert body:
// the impacted service, its host, and a deep link to the
// service page. Best-effort — missing pieces are simply omitted.
func ServiceAlertContext(db *sql.DB, serviceID int64) string {
	var name string
	var orgID int64
	var hostName sql.NullString
	err := db.QueryRow(`
		SELECT s.name, s.organization_id, COALESCE(h.name, s.host)
		FROM services s
		LEFT JOIN hosts h ON h.id = s.host_id
		WHERE s.id = $1`, serviceID).Scan(&name, &orgID, &hostName)
	if err != nil {
		return ""
	}

	var b strings.Builder
	if name != "" {
		fmt.Fprintf(&b, "\nService: %s", name)
	}
	if hostName.Valid && hostName.String != "" {
		fmt.Fprintf(&b, "\nHost: %s", hostName.String)
	}
	if url := serviceURL(db, orgID, serviceID); url != "" {
		fmt.Fprintf(&b, "\n%s", url)
	}
	return b.String()
}

// HostAlertContext is the host-targeted equivalent (rules scoped to a whole host).
func HostAlertContext(db *sql.DB, hostID int64) string {
	var name string
	var orgID int64
	if err := db.QueryRow(
		`SELECT name, organization_id FROM hosts WHERE id = $1`, hostID,
	).Scan(&name, &orgID); err != nil {
		return ""
	}
	var b strings.Builder
	if name != "" {
		fmt.Fprintf(&b, "\nHost: %s", name)
	}
	if base := frontendBaseURL(); base != "" {
		if slug := orgSlug(db, orgID); slug != "" {
			fmt.Fprintf(&b, "\n%s/organizations/%s/hosts/%d", base, slug, hostID)
		}
	}
	return b.String()
}

func serviceURL(db *sql.DB, orgID, serviceID int64) string {
	base := frontendBaseURL()
	if base == "" {
		return ""
	}
	slug := orgSlug(db, orgID)
	if slug == "" {
		return ""
	}
	return fmt.Sprintf("%s/organizations/%s/services/%d", base, slug, serviceID)
}

func orgSlug(db *sql.DB, orgID int64) string {
	var slug string
	if err := db.QueryRow(`SELECT slug FROM organizations WHERE id = $1`, orgID).Scan(&slug); err != nil {
		return ""
	}
	return slug
}
