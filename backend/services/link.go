package services

import (
	"database/sql"
	"fmt"

	"middle-monitor/backend/models"
)

// LinkService handles application link operations for alert correlation
type LinkService struct {
	db *sql.DB
}

func NewLinkService(db *sql.DB) *LinkService {
	return &LinkService{db: db}
}

// Create creates an application link. Validates that target_id exists (host,
// service, host group, or another application, in org). "service" and "app"
// both point at rows in the services table but are kept mutually exclusive:
// "service" must be a monitored check (never an Error SDK app) and "app" must
// be one (never a check), so the two concepts can't be confused with each other.
func (s *LinkService) Create(orgID int64, link models.ApplicationLink) (*models.ApplicationLink, error) {
	link.OrganizationID = orgID

	switch link.TargetType {
	case "host":
		var exists int
		if err := s.db.QueryRow(`SELECT 1 FROM hosts WHERE id = $1 AND organization_id = $2`, link.TargetID, orgID).Scan(&exists); err != nil {
			if err == sql.ErrNoRows {
				return nil, &NotInOrgError{Kind: "host", Ref: fmt.Sprint(link.TargetID)}
			}
			return nil, err
		}
	case "host_group":
		var exists int
		if err := s.db.QueryRow(`SELECT 1 FROM host_groups WHERE id = $1 AND organization_id = $2`, link.TargetID, orgID).Scan(&exists); err != nil {
			if err == sql.ErrNoRows {
				return nil, &NotInOrgError{Kind: "host group", Ref: fmt.Sprint(link.TargetID)}
			}
			return nil, err
		}
	case "service":
		var exists int
		if err := s.db.QueryRow(`SELECT 1 FROM services WHERE id = $1 AND organization_id = $2 AND type NOT LIKE 'error_service_%'`, link.TargetID, orgID).Scan(&exists); err != nil {
			if err == sql.ErrNoRows {
				return nil, &NotInOrgError{Kind: "service", Ref: fmt.Sprint(link.TargetID)}
			}
			return nil, err
		}
	case "app":
		// App targets are name-based: the app just has to be known to the org,
		// either as a registered error service or as a service tag that has
		// actually reported errors — registration is not required.
		if link.TargetAppName == "" {
			return nil, ErrLinkAppNameMissing
		}
		if link.TargetAppName == link.AppServiceName {
			return nil, ErrLinkSelfReference
		}
		var exists int
		err := s.db.QueryRow(`
			SELECT 1 WHERE EXISTS (
				SELECT 1 FROM services WHERE organization_id = $1 AND name = $2 AND type LIKE 'error_service_%'
			) OR EXISTS (
				SELECT 1 FROM application_errors WHERE organization_id = $1 AND service = $2
			)`, orgID, link.TargetAppName).Scan(&exists)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, &NotInOrgError{Kind: "application", Ref: link.TargetAppName}
			}
			return nil, err
		}
		link.TargetID = 0
	default:
		return nil, ErrLinkTargetType
	}

	query := `INSERT INTO application_links (organization_id, app_service_name, target_type, target_id, target_app_name)
			  VALUES ($1, $2, $3, $4, NULLIF($5, '')) RETURNING id`
	var id int64
	if err := s.db.QueryRow(query, link.OrganizationID, link.AppServiceName, link.TargetType, link.TargetID, link.TargetAppName).Scan(&id); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrLinkCreate, err)
	}
	link.ID = id
	return &link, nil
}

// ListForOrg returns all application links for the organization, with target names resolved
func (s *LinkService) ListForOrg(orgID int64) ([]models.ApplicationLink, error) {
	query := `SELECT id, organization_id, app_service_name, target_type, target_id, COALESCE(target_app_name, '')
			  FROM application_links WHERE organization_id = $1 ORDER BY app_service_name, target_type, target_id`
	rows, err := s.db.Query(query, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []models.ApplicationLink
	for rows.Next() {
		var l models.ApplicationLink
		if err := rows.Scan(&l.ID, &l.OrganizationID, &l.AppServiceName, &l.TargetType, &l.TargetID, &l.TargetAppName); err != nil {
			return nil, err
		}
		// Resolve target name
		switch l.TargetType {
		case "host":
			_ = s.db.QueryRow(`SELECT name FROM hosts WHERE id = $1`, l.TargetID).Scan(&l.TargetName)
		case "host_group":
			_ = s.db.QueryRow(`SELECT name FROM host_groups WHERE id = $1`, l.TargetID).Scan(&l.TargetName)
		case "app":
			l.TargetName = l.TargetAppName
			if l.TargetName == "" {
				// Legacy id-based app link created before the name migration.
				_ = s.db.QueryRow(`SELECT name FROM services WHERE id = $1`, l.TargetID).Scan(&l.TargetName)
			}
		default:
			_ = s.db.QueryRow(`SELECT name FROM services WHERE id = $1`, l.TargetID).Scan(&l.TargetName)
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

// GetLinksForCorrelation returns host IDs and service IDs linked to the given app (for correlation query)
func (s *LinkService) GetLinksForCorrelation(orgID int64, appServiceName string) (hostIDs []int64, serviceIDs []int64, err error) {
	query := `SELECT target_type, target_id FROM application_links
			  WHERE organization_id = $1 AND app_service_name = $2`
	rows, err := s.db.Query(query, orgID, appServiceName)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var targetType string
		var targetID int64
		if err := rows.Scan(&targetType, &targetID); err != nil {
			return nil, nil, err
		}
		if targetType == "host" {
			hostIDs = append(hostIDs, targetID)
		} else {
			serviceIDs = append(serviceIDs, targetID)
		}
	}
	return hostIDs, serviceIDs, rows.Err()
}

// Delete removes an application link by ID (and verifies org)
func (s *LinkService) Delete(id int64, orgID int64) error {
	res, err := s.db.Exec(`DELETE FROM application_links WHERE id = $1 AND organization_id = $2`, id, orgID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrLinkNotFound
	}
	return nil
}
