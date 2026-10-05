package services

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"
)

// InstallToken represents an agent install token
type InstallToken struct {
	ID             int64      `json:"id"`
	OrganizationID int64      `json:"organization_id"`
	Token          string     `json:"token,omitempty"`        // Full token only on create
	TokenPrefix    string     `json:"token_prefix,omitempty"` // First 8 chars for display in list
	Name           string     `json:"name,omitempty"`
	CreatedBy      *int64     `json:"created_by,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

// InstallTokenService handles install token operations
type InstallTokenService struct {
	db *sql.DB
}

func NewInstallTokenService(db *sql.DB) *InstallTokenService {
	return &InstallTokenService{db: db}
}

// ValidateToken returns organization_id if token is valid, error otherwise
func (s *InstallTokenService) ValidateToken(token string) (orgID int64, err error) {
	if token == "" {
		return 0, ErrTokenRequired
	}
	var expiresAt sql.NullTime
	err = s.db.QueryRow(`
		SELECT organization_id, expires_at
		FROM install_tokens
		WHERE token = $1
	`, token).Scan(&orgID, &expiresAt)
	if err == sql.ErrNoRows {
		return 0, ErrTokenInvalid
	}
	if err != nil {
		return 0, err
	}
	if expiresAt.Valid && expiresAt.Time.Before(time.Now()) {
		return 0, ErrTokenExpired
	}
	return orgID, nil
}

// CreateToken generates a new install token for the organization
func (s *InstallTokenService) CreateToken(orgID int64, name string, expiresAt *time.Time, createdBy *int64) (*InstallToken, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(b)

	var id int64
	var createdAt time.Time
	var nameOut sql.NullString
	var createdByOut sql.NullInt64
	var expiresAtOut sql.NullTime
	err := s.db.QueryRow(`
		INSERT INTO install_tokens (organization_id, token, name, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, organization_id, token, name, created_by, created_at, expires_at
	`, orgID, token, name, createdBy, expiresAt).Scan(
		&id, &orgID, &token, &nameOut, &createdByOut, &createdAt, &expiresAtOut,
	)
	if err != nil {
		return nil, err
	}

	t := &InstallToken{
		ID:             id,
		OrganizationID: orgID,
		Token:          token,
		CreatedAt:      createdAt,
	}
	if nameOut.Valid {
		t.Name = nameOut.String
	}
	if createdByOut.Valid {
		t.CreatedBy = &createdByOut.Int64
	}
	if expiresAtOut.Valid {
		t.ExpiresAt = &expiresAtOut.Time
	}
	return t, nil
}

// ListTokens returns all install tokens for an organization
func (s *InstallTokenService) ListTokens(orgID int64) ([]InstallToken, error) {
	rows, err := s.db.Query(`
		SELECT id, organization_id, token, name, created_by, created_at, expires_at
		FROM install_tokens
		WHERE organization_id = $1
		ORDER BY created_at DESC
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []InstallToken
	for rows.Next() {
		var t InstallToken
		var token string
		var name sql.NullString
		var createdBy sql.NullInt64
		var expiresAt sql.NullTime
		if err := rows.Scan(&t.ID, &t.OrganizationID, &token, &name, &createdBy, &t.CreatedAt, &expiresAt); err != nil {
			return nil, err
		}
		if len(token) >= 8 {
			t.TokenPrefix = token[:8]
		} else {
			t.TokenPrefix = token
		}
		if name.Valid {
			t.Name = name.String
		}
		if createdBy.Valid {
			t.CreatedBy = &createdBy.Int64
		}
		if expiresAt.Valid {
			t.ExpiresAt = &expiresAt.Time
		}
		tokens = append(tokens, t)
	}
	return tokens, nil
}

// DeleteToken removes an install token
func (s *InstallTokenService) DeleteToken(id int64, orgID int64) error {
	result, err := s.db.Exec(`DELETE FROM install_tokens WHERE id = $1 AND organization_id = $2`, id, orgID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrTokenNotFound
	}
	return nil
}
