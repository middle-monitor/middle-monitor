package services

import (
	"encoding/json"
	"fmt"

	"middle-monitor/backend/internal/credentialenc"
	"middle-monitor/backend/models"
)

const httpAuthVersion = 1

// Stored HTTP auth (credentials column JSON). Mode is stored in clear; secrets are encrypted.
type httpAuthStored struct {
	Version int                 `json:"version"`
	Mode    string              `json:"mode"` // none | bearer | basic
	Secret  *credentialenc.Blob `json:"secret,omitempty"`
}

type httpAuthIncoming struct {
	Mode          string `json:"mode"`
	BearerToken   string `json:"bearer_token"`
	BasicUser     string `json:"basic_user"`
	BasicPassword string `json:"basic_password"`
}

type credentialsIncoming struct {
	HTTPAuth *httpAuthIncoming `json:"http_auth"`
}

// WorkerHTTPAuth is the decrypted auth used by the HTTP check worker.
type WorkerHTTPAuth struct {
	Mode          string
	BearerToken   string
	BasicUser     string
	BasicPassword string
}

// PrepareHTTPServiceCredentials normalizes credentials for http services before DB write.
// existing is required for updates (merge / preserve secrets).
func PrepareHTTPServiceCredentials(s *models.Service, existing *models.Service, isCreate bool) error {
	if s == nil || s.Type != "http" {
		sanitizeAPIOnlyFields(s)
		return nil
	}

	preserve := s.PreserveHTTPSecrets != nil && *s.PreserveHTTPSecrets
	sanitizeAPIOnlyFields(s)

	// Update: empty/absent credentials in the request means "leave stored value unchanged"
	if !isCreate && (s.Credentials == nil || (s.Credentials != nil && *s.Credentials == "")) {
		if existing != nil && existing.Credentials != nil {
			s.Credentials = existing.Credentials
		} else {
			s.Credentials = nil
		}
		return nil
	}

	incoming, err := parseHTTPCredentialsIncoming(s.Credentials)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrHTTPAuthInvalid, err)
	}

	if incoming == nil || incoming.Mode == "" || incoming.Mode == "none" {
		s.Credentials = nil
		return nil
	}

	ring, err := credentialenc.Global()
	if err != nil {
		return err
	}

	switch incoming.Mode {
	case "bearer":
		token := incoming.BearerToken
		if token == "" && preserve && existing != nil && existing.Credentials != nil {
			prev, err := decryptHTTPAuthSecrets(existing.Credentials, ring)
			if err != nil {
				return fmt.Errorf("bearer token: %w: %w", ErrHTTPAuthPreserve, err)
			}
			token = prev.BearerToken
		}
		if token == "" {
			return ErrHTTPAuthBearerMissing
		}
		plain, err := json.Marshal(map[string]string{"token": token})
		if err != nil {
			return err
		}
		blob, err := ring.Encrypt(plain)
		if err != nil {
			return err
		}
		out, err := json.Marshal(map[string]any{
			"http_auth": httpAuthStored{
				Version: httpAuthVersion,
				Mode:    "bearer",
				Secret:  blob,
			},
		})
		if err != nil {
			return err
		}
		str := string(out)
		s.Credentials = &str
		return nil

	case "basic":
		user := incoming.BasicUser
		pass := incoming.BasicPassword
		if preserve && existing != nil && existing.Credentials != nil {
			prev, err := decryptHTTPAuthSecrets(existing.Credentials, ring)
			if err != nil {
				return fmt.Errorf("basic auth: %w: %w", ErrHTTPAuthPreserve, err)
			}
			if user == "" {
				user = prev.BasicUser
			}
			if pass == "" {
				pass = prev.BasicPassword
			}
		}
		if user == "" || pass == "" {
			return ErrHTTPAuthBasicMissing
		}
		plain, err := json.Marshal(map[string]string{"user": user, "password": pass})
		if err != nil {
			return err
		}
		blob, err := ring.Encrypt(plain)
		if err != nil {
			return err
		}
		out, err := json.Marshal(map[string]any{
			"http_auth": httpAuthStored{
				Version: httpAuthVersion,
				Mode:    "basic",
				Secret:  blob,
			},
		})
		if err != nil {
			return err
		}
		str := string(out)
		s.Credentials = &str
		return nil

	default:
		return &UnknownHTTPAuthModeError{Mode: incoming.Mode}
	}
}

func sanitizeAPIOnlyFields(s *models.Service) {
	if s == nil {
		return
	}
	s.PreserveHTTPSecrets = nil
}

func parseHTTPCredentialsIncoming(raw *string) (*httpAuthIncoming, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	var wrap credentialsIncoming
	if err := json.Unmarshal([]byte(*raw), &wrap); err != nil {
		return nil, err
	}
	if wrap.HTTPAuth == nil {
		return nil, nil
	}
	return wrap.HTTPAuth, nil
}

// ApplyHTTPAuthAPIRedaction clears raw credentials for http services and exposes safe metadata only.
func ApplyHTTPAuthAPIRedaction(s *models.Service) {
	if s == nil || s.Type != "http" {
		return
	}
	sanitizeAPIOnlyFields(s)

	mode := "none"
	configured := false
	if s.Credentials != nil && *s.Credentials != "" {
		var outer struct {
			HTTPAuth *httpAuthStored `json:"http_auth"`
		}
		if err := json.Unmarshal([]byte(*s.Credentials), &outer); err == nil && outer.HTTPAuth != nil {
			mode = outer.HTTPAuth.Mode
			if mode == "" {
				mode = "none"
			}
			if mode != "none" && outer.HTTPAuth.Secret != nil {
				configured = true
			}
		}
	}
	s.Credentials = nil
	modeCopy := mode
	s.HttpAuthMode = &modeCopy
	cfg := configured
	s.HttpAuthConfigured = &cfg
}

// DecryptHTTPAuthForWorker loads stored credentials for an HTTP check.
func DecryptHTTPAuthForWorker(credentials *string, ring *credentialenc.KeyRing) (*WorkerHTTPAuth, error) {
	if credentials == nil || *credentials == "" {
		return &WorkerHTTPAuth{Mode: "none"}, nil
	}
	if ring == nil {
		r, err := credentialenc.Global()
		if err != nil {
			return nil, err
		}
		ring = r
	}
	return decryptHTTPAuthSecrets(credentials, ring)
}

func decryptHTTPAuthSecrets(credentials *string, ring *credentialenc.KeyRing) (*WorkerHTTPAuth, error) {
	var outer struct {
		HTTPAuth *httpAuthStored `json:"http_auth"`
	}
	if err := json.Unmarshal([]byte(*credentials), &outer); err != nil {
		return nil, err
	}
	if outer.HTTPAuth == nil || outer.HTTPAuth.Mode == "" || outer.HTTPAuth.Mode == "none" {
		return &WorkerHTTPAuth{Mode: "none"}, nil
	}
	if outer.HTTPAuth.Secret == nil {
		return nil, ErrHTTPAuthBlobMissing
	}
	plain, err := ring.Decrypt(outer.HTTPAuth.Secret)
	if err != nil {
		return nil, err
	}
	w := &WorkerHTTPAuth{Mode: outer.HTTPAuth.Mode}
	switch outer.HTTPAuth.Mode {
	case "bearer":
		var m map[string]string
		if err := json.Unmarshal(plain, &m); err != nil {
			return nil, err
		}
		w.BearerToken = m["token"]
	case "basic":
		var m map[string]string
		if err := json.Unmarshal(plain, &m); err != nil {
			return nil, err
		}
		w.BasicUser = m["user"]
		w.BasicPassword = m["password"]
	default:
		return nil, &UnknownHTTPAuthModeError{Mode: outer.HTTPAuth.Mode}
	}
	return w, nil
}
