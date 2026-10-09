package services

import (
	"database/sql"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// DeleteOrganization removes an organization and every row it owns. Members who
// belong to other organizations are moved to one of them; members left with no
// organization are deleted with it.
func (s *AuthService) DeleteOrganization(orgID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrOrgDelete, err)
	}
	defer tx.Rollback()

	// users.organization_id cascades: without the repoint, deleting the org would
	// also delete members whose home org it is, whatever else they belong to.
	if _, err := tx.Exec(`
		UPDATE users u SET organization_id = (
			SELECT m.organization_id FROM memberships m
			WHERE m.user_id = u.id AND m.organization_id <> $1
			ORDER BY m.created_at ASC LIMIT 1
		)
		WHERE u.organization_id = $1
		  AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.organization_id <> $1)
	`, orgID); err != nil {
		return fmt.Errorf("%w: %w", ErrOrgDelete, err)
	}
	if _, err := tx.Exec(`
		DELETE FROM users u
		WHERE EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.organization_id = $1)
		  AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = u.id AND m.organization_id <> $1)
	`, orgID); err != nil {
		return fmt.Errorf("%w: %w", ErrOrgDelete, err)
	}
	result, err := tx.Exec(`DELETE FROM organizations WHERE id = $1`, orgID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrOrgDelete, err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return ErrOrgNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: %w", ErrOrgDelete, err)
	}
	return nil
}

// OrgsDeletedWithAccount lists the organizations a user is the only member of,
// which go away with the account. It refuses when the user is the last admin of
// an organization that has other members, since nobody could administer it.
func (s *AuthService) OrgsDeletedWithAccount(userID int64) ([]int64, error) {
	rows, err := s.db.Query(`
		SELECT m.organization_id, m.role,
		       (SELECT COUNT(*) FROM memberships o WHERE o.organization_id = m.organization_id),
		       (SELECT COUNT(*) FROM memberships o WHERE o.organization_id = m.organization_id AND o.role = 'admin')
		FROM memberships m WHERE m.user_id = $1
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUserOrganizationsFetch, err)
	}
	defer rows.Close()

	var orgIDs []int64
	for rows.Next() {
		var orgID int64
		var role string
		var members, admins int
		if err := rows.Scan(&orgID, &role, &members, &admins); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrUserOrganizationScan, err)
		}
		switch {
		case members == 1:
			orgIDs = append(orgIDs, orgID)
		case role == RoleAdmin && admins == 1:
			return nil, ErrLastAdmin
		}
	}
	return orgIDs, rows.Err()
}

// DeleteAccount removes a user identity; its memberships cascade.
func (s *AuthService) DeleteAccount(userID int64) error {
	if _, err := s.db.Exec(`DELETE FROM users WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("%w: %w", ErrAccountDelete, err)
	}
	return nil
}

// CheckPassword confirms the user's current password before a destructive action.
func (s *AuthService) CheckPassword(userID int64, password string) error {
	var hash string
	err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&hash)
	if err == sql.ErrNoRows {
		return ErrUserNotFound
	} else if err != nil {
		return fmt.Errorf("%w: %w", ErrUserFind, err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return ErrInvalidCredentials
	}
	return nil
}
