package services

import (
	"database/sql"
	"encoding/json"

	"middle-monitor/backend/internal/credentialenc"
)

// SMTPSettings is a resolved, ready-to-use SMTP configuration for one organization.
type SMTPSettings struct {
	Host string
	Port string
	User string
	Pass string
	From string
}

// Usable reports whether the settings can actually deliver mail (host + port set).
func (s *SMTPSettings) Usable() bool {
	return s != nil && s.Host != "" && s.Port != ""
}

// OrgSMTPProvider resolves an organization's SMTP settings (password decrypted).
// It is a package-level hook wired at server startup so the DB-less notification
// layer can look up per-org SMTP without importing the database package.
// It returns (nil, false) when the org has no usable SMTP configuration.
var OrgSMTPProvider func(orgID int64) (*SMTPSettings, bool)

// OrgMemberEmails returns the lowercased verified emails of an organization's
// members. Wired with OrgSMTPProvider.
var OrgMemberEmails func(orgID int64) map[string]bool

// InitOrgSMTPProvider wires OrgSMTPProvider to a database. Each process that can
// dispatch email alerts (api, receiver, worker) calls this at startup.
func InitOrgSMTPProvider(db *sql.DB) {
	OrgSMTPProvider = func(orgID int64) (*SMTPSettings, bool) {
		var host, port, user, from, passEnc string
		if err := db.QueryRow(
			`SELECT smtp_host, smtp_port, smtp_user, smtp_from, smtp_pass_enc FROM organizations WHERE id = $1`,
			orgID,
		).Scan(&host, &port, &user, &from, &passEnc); err != nil || host == "" || port == "" {
			return nil, false
		}
		pass, err := DecryptSMTPPassword(passEnc)
		if err != nil {
			return nil, false
		}
		return &SMTPSettings{Host: host, Port: port, User: user, Pass: pass, From: from}, true
	}
	OrgMemberEmails = func(orgID int64) map[string]bool {
		rows, err := db.Query(`
			SELECT LOWER(u.email) FROM memberships m
			JOIN users u ON u.id = m.user_id
			WHERE m.organization_id = $1 AND u.email_verified`, orgID)
		if err != nil {
			return nil
		}
		defer rows.Close()
		emails := map[string]bool{}
		for rows.Next() {
			var email string
			if rows.Scan(&email) == nil {
				emails[email] = true
			}
		}
		return emails
	}
}

// smtpPassStored is the on-disk shape of the SMTP password: either an encrypted
// blob (preferred) or, when no key ring is configured, the plaintext (mirroring
// the behaviour of the other credential stores in this package).
type smtpPassStored struct {
	Enc   *credentialenc.Blob `json:"enc,omitempty"`
	Plain string              `json:"plain,omitempty"`
}

// EncryptSMTPPassword returns the value to persist in organizations.smtp_pass_enc.
// An empty input yields an empty string (no password set).
func EncryptSMTPPassword(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	stored := smtpPassStored{}
	if ring, err := credentialenc.Global(); err == nil && ring.MustHaveActiveKey() == nil {
		blob, encErr := ring.Encrypt([]byte(plain))
		if encErr != nil {
			return "", encErr
		}
		stored.Enc = blob
	} else {
		stored.Plain = plain
	}
	out, err := json.Marshal(stored)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// DecryptSMTPPassword reverses EncryptSMTPPassword.
func DecryptSMTPPassword(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	var s smtpPassStored
	if err := json.Unmarshal([]byte(stored), &s); err != nil {
		return "", err
	}
	if s.Enc != nil {
		ring, err := credentialenc.Global()
		if err != nil {
			return "", err
		}
		plain, err := ring.Decrypt(s.Enc)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	}
	return s.Plain, nil
}
