package services

import (
	"database/sql"
	"fmt"
	"strings"

	"middle-monitor/backend/models"
)

// HostGroupService manages host groups, which scope alert correlation to a host
// and the other hosts sharing the same logical role.
type HostGroupService struct {
	db *sql.DB
}

func NewHostGroupService(db *sql.DB) *HostGroupService {
	return &HostGroupService{db: db}
}

// ListForOrg returns the org's host groups (default first), each with its host count.
func (s *HostGroupService) ListForOrg(orgID int64) ([]models.HostGroup, error) {
	rows, err := s.db.Query(`
		SELECT hg.id, hg.organization_id, hg.name, hg.display_name, hg.is_default, hg.created_at,
		       (SELECT COUNT(*) FROM hosts h WHERE h.host_group_id = hg.id) AS host_count
		FROM host_groups hg
		WHERE hg.organization_id = $1
		ORDER BY hg.is_default DESC, LOWER(hg.name)`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []models.HostGroup{}
	for rows.Next() {
		var g models.HostGroup
		var displayName sql.NullString
		if err := rows.Scan(&g.ID, &g.OrganizationID, &g.Name, &displayName, &g.IsDefault, &g.CreatedAt, &g.HostCount); err != nil {
			return nil, err
		}
		if displayName.Valid {
			g.DisplayName = &displayName.String
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// Create adds a non-default host group. Names are unique within an organization.
func (s *HostGroupService) Create(orgID int64, name string, displayName *string) (*models.HostGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrNameRequired
	}
	var g models.HostGroup
	var dn sql.NullString
	err := s.db.QueryRow(`
		INSERT INTO host_groups (organization_id, name, display_name, is_default)
		VALUES ($1, $2, $3, false)
		RETURNING id, organization_id, name, display_name, is_default, created_at`,
		orgID, name, toNullString(displayName)).Scan(&g.ID, &g.OrganizationID, &g.Name, &dn, &g.IsDefault, &g.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique") {
			return nil, &NameTakenError{Kind: "host group", Name: name}
		}
		return nil, fmt.Errorf("%w: %w", ErrHostGroupCreate, err)
	}
	if dn.Valid {
		g.DisplayName = &dn.String
	}
	return &g, nil
}

// Update changes a group's name and display name (the default group included).
func (s *HostGroupService) Update(orgID, id int64, name string, displayName *string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrNameRequired
	}
	res, err := s.db.Exec(`UPDATE host_groups SET name = $1, display_name = $2 WHERE id = $3 AND organization_id = $4`,
		name, toNullString(displayName), id, orgID)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique") {
			return &NameTakenError{Kind: "host group", Name: name}
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrHostGroupNotFound
	}
	return nil
}

// Delete removes a non-default, empty group. Deleting a group that still has
// hosts is refused so no host silently changes correlation scope.
func (s *HostGroupService) Delete(orgID, id int64) error {
	var isDefault bool
	if err := s.db.QueryRow(`SELECT is_default FROM host_groups WHERE id = $1 AND organization_id = $2`, id, orgID).Scan(&isDefault); err != nil {
		if err == sql.ErrNoRows {
			return ErrHostGroupNotFound
		}
		return err
	}
	if isDefault {
		return ErrDefaultGroupUndeletable
	}
	var hostCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM hosts WHERE host_group_id = $1 AND organization_id = $2`, id, orgID).Scan(&hostCount); err != nil {
		return err
	}
	if hostCount > 0 {
		return ErrGroupHasHosts
	}
	if _, err := s.db.Exec(`DELETE FROM host_groups WHERE id = $1 AND organization_id = $2`, id, orgID); err != nil {
		return err
	}
	return nil
}

// AssignHost moves a host into a group (both must belong to the organization).
func (s *HostGroupService) AssignHost(orgID, hostID, groupID int64) error {
	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM host_groups WHERE id = $1 AND organization_id = $2`, groupID, orgID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return ErrHostGroupNotFound
		}
		return err
	}
	res, err := s.db.Exec(`UPDATE hosts SET host_group_id = $1 WHERE id = $2 AND organization_id = $3`, groupID, hostID, orgID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrHostNotFound
	}
	return nil
}
