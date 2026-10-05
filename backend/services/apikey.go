package services

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"middle-monitor/backend/models"
)

type APIKeyService struct {
	db *sql.DB
}

func NewAPIKeyService(db *sql.DB) *APIKeyService {
	return &APIKeyService{db: db}
}

func generateAPIKey() (string, string, string) {
	// Generate 32 random bytes
	b := make([]byte, 32)
	rand.Read(b)
	key := "mm_" + hex.EncodeToString(b)
	prefix := key[:10]

	// Hash the key for storage
	hash := sha256.Sum256([]byte(key))
	keyHash := hex.EncodeToString(hash[:])

	return key, prefix, keyHash
}

func (s *APIKeyService) CreateKey(orgID int64, name, scopes string, expiresAt *time.Time, createdBy *int64) (*models.APIKeyWithSecret, error) {
	key, prefix, keyHash := generateAPIKey()

	query := `INSERT INTO api_keys (organization_id, name, key_hash, key_prefix, scopes, expires_at, created_by)
			  VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id, created_at`
	var apiKey models.APIKeyWithSecret
	apiKey.OrganizationID = orgID
	apiKey.Name = name
	apiKey.KeyPrefix = prefix
	apiKey.Scopes = scopes
	apiKey.ExpiresAt = expiresAt
	apiKey.CreatedBy = createdBy
	apiKey.Key = key

	err := s.db.QueryRow(query, orgID, name, keyHash, prefix, scopes, expiresAt, createdBy).Scan(&apiKey.ID, &apiKey.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAPIKeyCreate, err)
	}

	return &apiKey, nil
}

// CreatePersonalKey creates an API token owned by a user. Unlike an org key, it
// carries user_id so the API authenticates AS that user (with their role). scopes
// is stored as "personal" purely for display; the effective permissions come from
// the user's role at validation time.
func (s *APIKeyService) CreatePersonalKey(orgID, userID int64, name string, expiresAt *time.Time) (*models.APIKeyWithSecret, error) {
	key, prefix, keyHash := generateAPIKey()

	query := `INSERT INTO api_keys (organization_id, user_id, name, key_hash, key_prefix, scopes, expires_at, created_by)
			  VALUES ($1, $2, $3, $4, $5, 'personal', $6, $2) RETURNING id, created_at`
	var apiKey models.APIKeyWithSecret
	apiKey.OrganizationID = orgID
	apiKey.Name = name
	apiKey.KeyPrefix = prefix
	apiKey.Scopes = "personal"
	apiKey.ExpiresAt = expiresAt
	apiKey.CreatedBy = &userID
	apiKey.Key = key

	if err := s.db.QueryRow(query, orgID, userID, name, keyHash, prefix, expiresAt).Scan(&apiKey.ID, &apiKey.CreatedAt); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPersonalAPIKeyCreate, err)
	}
	return &apiKey, nil
}

// APIKeyIdentity is the resolved owner of a validated API key. UserID is set only
// for personal tokens (the key authenticates as that user with Role); for org
// tokens UserID is nil and Role is empty (the caller decides the org-token role).
type APIKeyIdentity struct {
	OrgID   int64
	OrgSlug string
	UserID  *int64
	Role    string
	Email   string
}

// ValidateKey verifies a raw API key ("mm_..."), rejects expired ones, records
// last_used_at, and returns the resolved identity (org + optional owning user).
// This is what makes the "last used" column update and lets API keys authenticate
// the REST API.
func (s *APIKeyService) ValidateKey(rawKey string) (*APIKeyIdentity, error) {
	rawKey = strings.TrimSpace(rawKey)
	if !strings.HasPrefix(rawKey, "mm_") {
		return nil, ErrNotAPIKey
	}
	sum := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(sum[:])

	var id int64
	var ident APIKeyIdentity
	var expiresAt sql.NullTime
	var userID sql.NullInt64
	var role, email sql.NullString
	err := s.db.QueryRow(`
		SELECT k.id, k.organization_id, o.slug, k.expires_at, k.user_id, u.role, u.email
		FROM api_keys k
		JOIN organizations o ON o.id = k.organization_id
		LEFT JOIN users u ON u.id = k.user_id
		WHERE k.key_hash = $1`, keyHash).Scan(&id, &ident.OrgID, &ident.OrgSlug, &expiresAt, &userID, &role, &email)
	if err == sql.ErrNoRows {
		return nil, ErrAPIKeyInvalid
	}
	if err != nil {
		return nil, err
	}
	if expiresAt.Valid && time.Now().After(expiresAt.Time) {
		return nil, ErrAPIKeyExpired
	}
	if userID.Valid {
		uid := userID.Int64
		ident.UserID = &uid
		ident.Role = role.String
		ident.Email = email.String
	}

	// Best-effort: stamp last_used_at so the UI shows real usage.
	_, _ = s.db.Exec(`UPDATE api_keys SET last_used_at = NOW() WHERE id = $1`, id)
	return &ident, nil
}

// GetKeys returns the org's API keys, excluding personal tokens (those belong to a
// user and are listed in their account settings, not org settings).
func (s *APIKeyService) GetKeys(orgID int64) ([]models.APIKey, error) {
	query := `SELECT id, organization_id, name, key_prefix, scopes, last_used_at, expires_at, created_by, created_at
			  FROM api_keys WHERE organization_id = $1 AND user_id IS NULL ORDER BY created_at DESC`
	return s.scanKeys(query, orgID)
}

// GetPersonalKeys returns the API tokens owned by a specific user.
func (s *APIKeyService) GetPersonalKeys(userID int64) ([]models.APIKey, error) {
	query := `SELECT id, organization_id, name, key_prefix, scopes, last_used_at, expires_at, created_by, created_at
			  FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC`
	return s.scanKeys(query, userID)
}

func (s *APIKeyService) scanKeys(query string, arg int64) ([]models.APIKey, error) {
	rows, err := s.db.Query(query, arg)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAPIKeysFetch, err)
	}
	defer rows.Close()

	var keys []models.APIKey
	for rows.Next() {
		var k models.APIKey
		if err := rows.Scan(&k.ID, &k.OrganizationID, &k.Name, &k.KeyPrefix, &k.Scopes, &k.LastUsedAt, &k.ExpiresAt, &k.CreatedBy, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrAPIKeyScan, err)
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func (s *APIKeyService) DeleteKey(keyID, orgID int64) error {
	result, err := s.db.Exec(`DELETE FROM api_keys WHERE id = $1 AND organization_id = $2 AND user_id IS NULL`, keyID, orgID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrAPIKeyDelete, err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
}

// DeletePersonalKey deletes a personal token, scoped to its owner so a user can
// only revoke their own.
func (s *APIKeyService) DeletePersonalKey(keyID, userID int64) error {
	result, err := s.db.Exec(`DELETE FROM api_keys WHERE id = $1 AND user_id = $2`, keyID, userID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrAPIKeyDelete, err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAPIKeyNotFound
	}
	return nil
}
