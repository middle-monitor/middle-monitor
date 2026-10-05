package services

import (
	"encoding/json"

	"middle-monitor/backend/internal/credentialenc"
)

// sqlCredsStored is the format persisted in the credentials JSON column for SQL services.
// The password is replaced by an encrypted blob; other fields remain in clear.
type sqlCredsStored struct {
	Engine      string              `json:"engine,omitempty"`
	Host        string              `json:"host"`
	Port        string              `json:"port"`
	Database    string              `json:"database"`
	Username    string              `json:"username,omitempty"`
	PasswordEnc *credentialenc.Blob `json:"password_enc,omitempty"`
}

// sqlCredsRaw is the shape the frontend sends (password in clear).
type sqlCredsRaw struct {
	Engine   string `json:"engine,omitempty"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	Database string `json:"database"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// EncryptSQLCredentials takes the raw credentials JSON sent by the frontend,
// encrypts the password field, and returns the credentials JSON to persist.
// If encryption is not configured, the credentials are stored as-is.
func EncryptSQLCredentials(credentialsJSON string) (string, error) {
	if credentialsJSON == "" {
		return credentialsJSON, nil
	}

	var raw sqlCredsRaw
	if err := json.Unmarshal([]byte(credentialsJSON), &raw); err != nil {
		return credentialsJSON, nil // not SQL format — leave unchanged
	}

	stored := sqlCredsStored{
		Engine:   raw.Engine,
		Host:     raw.Host,
		Port:     raw.Port,
		Database: raw.Database,
		Username: raw.Username,
	}

	if raw.Password != "" {
		ring, err := credentialenc.Global()
		if err != nil {
			// Encryption not configured: fall through and store password in clear.
			// Return original JSON unchanged so the worker can still read it.
			return credentialsJSON, nil
		}
		if keyErr := ring.MustHaveActiveKey(); keyErr != nil {
			// No active key — store without encryption.
			return credentialsJSON, nil
		}
		blob, err := ring.Encrypt([]byte(raw.Password))
		if err != nil {
			return "", err
		}
		stored.PasswordEnc = blob
	}

	out, err := json.Marshal(stored)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// DecryptSQLPassword returns the plaintext password from stored SQL credentials.
// It handles both the encrypted format (password_enc) and legacy plain-text format (password).
func DecryptSQLPassword(credentialsJSON string) (string, error) {
	if credentialsJSON == "" {
		return "", nil
	}

	// Try encrypted format first.
	var stored sqlCredsStored
	if err := json.Unmarshal([]byte(credentialsJSON), &stored); err != nil {
		return "", err
	}
	if stored.PasswordEnc != nil {
		ring, err := credentialenc.Global()
		if err != nil {
			return "", ErrSQLKeyRingMissing
		}
		plain, err := ring.Decrypt(stored.PasswordEnc)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	}

	// Legacy: password stored in clear text.
	var raw sqlCredsRaw
	if err := json.Unmarshal([]byte(credentialsJSON), &raw); err != nil {
		return "", err
	}
	return raw.Password, nil
}
