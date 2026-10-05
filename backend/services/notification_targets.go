package services

import (
	"database/sql"
	"fmt"
)

// IncidentURL is the deep link a receiver puts in the ticket it opens. Empty
// when FRONTEND_URL is unset, rather than a link that goes nowhere.
func IncidentURL(db *sql.DB, orgID, incidentID int64) string {
	base := frontendBaseURL()
	if base == "" {
		return ""
	}
	slug := orgSlug(db, orgID)
	if slug == "" {
		return ""
	}
	return fmt.Sprintf("%s/organizations/%s/incidents/%d", base, slug, incidentID)
}

// FillEventTargets resolves the ids an event carries into the names a receiver
// can route on. Best-effort: a missing piece is left empty rather than failing
// the notification, which is the part that actually matters.
func FillEventTargets(db *sql.DB, event *NotificationEvent) {
	if event.OrganizationID != 0 && event.OrganizationSlug == "" {
		event.OrganizationSlug = orgSlug(db, event.OrganizationID)
	}

	if event.Host != nil && event.Host.ID != 0 {
		var name, service sql.NullString
		var group sql.NullString
		err := db.QueryRow(`
			SELECT h.name, h.service, g.name
			  FROM hosts h
			  LEFT JOIN host_groups g ON g.id = h.host_group_id
			 WHERE h.id = $1`, event.Host.ID).Scan(&name, &service, &group)
		if err == nil {
			event.Host.Name = name.String
			event.Host.Service = service.String
			event.Host.Group = group.String
		}
	}

	if event.Service != nil && event.Service.ID != 0 {
		var name, serviceType sql.NullString
		err := db.QueryRow(`SELECT name, type FROM services WHERE id = $1`, event.Service.ID).Scan(&name, &serviceType)
		if err == nil {
			if event.Service.Name == "" {
				event.Service.Name = name.String
			}
			event.Service.Type = serviceType.String
		}
	}
}
