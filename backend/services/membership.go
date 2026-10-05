package services

import (
	"database/sql"
	"fmt"

	"middle-monitor/backend/models"
)

// AddMembership grants a user a role in an organization. Idempotent: re-inviting
// an existing member updates their role rather than failing.
func (s *AuthService) AddMembership(userID, orgID int64, role string) error {
	if !IsValidRole(role) {
		role = RoleReadWrite
	}
	_, err := s.db.Exec(`
		INSERT INTO memberships (user_id, organization_id, role, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id, organization_id) DO UPDATE SET role = EXCLUDED.role
	`, userID, orgID, role)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMembershipAdd, err)
	}
	return nil
}

// RemoveMembership revokes a user's access to an organization. It does not delete
// the user identity (which may still belong to other orgs).
func (s *AuthService) RemoveMembership(userID, orgID int64) error {
	_, err := s.db.Exec(`DELETE FROM memberships WHERE user_id = $1 AND organization_id = $2`, userID, orgID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMembershipRemove, err)
	}
	return nil
}

// GetMembershipRole returns the user's role in an organization and whether such a
// membership exists. Used to gate access and stamp the active role in the JWT.
func (s *AuthService) GetMembershipRole(userID, orgID int64) (string, bool, error) {
	var role string
	err := s.db.QueryRow(`SELECT role FROM memberships WHERE user_id = $1 AND organization_id = $2`, userID, orgID).Scan(&role)
	if err == sql.ErrNoRows {
		return "", false, nil
	} else if err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrMembershipRoleFetch, err)
	}
	return role, true, nil
}

// GetUserOrganizations lists every organization a user belongs to, with their
// role in each, most recently joined first. Drives the login org selection and
// the in-app org switcher.
func (s *AuthService) GetUserOrganizations(userID int64) ([]models.UserOrganization, error) {
	rows, err := s.db.Query(`
		SELECT o.id, o.name, o.slug, COALESCE(o.plan, 'free'), o.trial_ends_at, COALESCE(o.mfa_required, false), o.created_at, o.updated_at, m.role
		FROM memberships m
		JOIN organizations o ON o.id = m.organization_id
		WHERE m.user_id = $1
		ORDER BY m.created_at ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUserOrganizationsFetch, err)
	}
	defer rows.Close()

	var orgs []models.UserOrganization
	for rows.Next() {
		var uo models.UserOrganization
		var trialEndsAt sql.NullTime
		if err := rows.Scan(&uo.ID, &uo.Name, &uo.Slug, &uo.Plan, &trialEndsAt, &uo.MFARequired, &uo.CreatedAt, &uo.UpdatedAt, &uo.Role); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrUserOrganizationScan, err)
		}
		ApplyTrial(&uo.Organization, trialEndsAt)
		orgs = append(orgs, uo)
	}
	return orgs, nil
}

// GetUserInOrg returns a user's identity scoped to a specific organization they
// belong to, with the per-org membership role. It is the membership-aware
// counterpart of GetUserByID, used to mint a session for the active org (login
// default org, refresh, org switch). Returns ErrUserNotFound when the user is not
// a member of that org.
func (s *AuthService) GetUserInOrg(userID, orgID int64) (*models.UserWithOrg, error) {
	var user models.User
	var org models.Organization
	var trialEndsAt sql.NullTime
	err := s.db.QueryRow(`
		SELECT u.id, u.email, u.name, m.role, COALESCE(u.email_verified, false), COALESCE(u.totp_enabled, false), u.created_at, u.updated_at, u.last_login_at,
		       o.id, o.name, o.slug, COALESCE(o.plan, 'free'), o.trial_ends_at, COALESCE(o.mfa_required, false), o.created_at, o.updated_at
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		JOIN organizations o ON o.id = m.organization_id
		WHERE m.user_id = $1 AND m.organization_id = $2
	`, userID, orgID).Scan(
		&user.ID, &user.Email, &user.Name, &user.Role, &user.EmailVerified, &user.TOTPEnabled, &user.CreatedAt, &user.UpdatedAt, &user.LastLoginAt,
		&org.ID, &org.Name, &org.Slug, &org.Plan, &trialEndsAt, &org.MFARequired, &org.CreatedAt, &org.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	} else if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUserInOrgFetch, err)
	}
	ApplyTrial(&org, trialEndsAt)
	user.OrganizationID = org.ID
	return &models.UserWithOrg{User: user, Organization: org}, nil
}

// SwitchOrganization issues a fresh session for another organization the user
// already belongs to. The user is already authenticated (and, if needed, has
// passed 2FA this session), so no second factor is re-requested here.
func (s *AuthService) SwitchOrganization(userID, targetOrgID int64) (*models.UserWithOrg, *models.AuthTokens, error) {
	userWithOrg, err := s.GetUserInOrg(userID, targetOrgID)
	if err != nil {
		return nil, nil, err
	}
	tokens, err := s.generateTokens(&userWithOrg.User, userWithOrg.Organization.Slug, userWithOrg.Organization.MFARequired)
	if err != nil {
		return nil, nil, err
	}
	return userWithOrg, tokens, nil
}
