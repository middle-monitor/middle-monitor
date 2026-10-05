package services

import (
	"encoding/json"
	"testing"

	"middle-monitor/backend/internal/credentialenc"
	"middle-monitor/backend/models"
)

func TestSanitizeAPIOnlyFields_Nil(t *testing.T) {
	// Should not panic on nil.
	sanitizeAPIOnlyFields(nil)
}

func TestSanitizeAPIOnlyFields_ClearsPreserveFlag(t *testing.T) {
	tr := true
	s := &models.Service{PreserveHTTPSecrets: &tr}
	sanitizeAPIOnlyFields(s)
	if s.PreserveHTTPSecrets != nil {
		t.Fatal("expected PreserveHTTPSecrets to be nil after sanitize")
	}
}

func TestParseHTTPCredentialsIncoming_Nil(t *testing.T) {
	got, err := parseHTTPCredentialsIncoming(nil)
	if err != nil || got != nil {
		t.Fatalf("expected nil,nil got %v,%v", got, err)
	}
}

func TestParseHTTPCredentialsIncoming_Empty(t *testing.T) {
	s := ""
	got, err := parseHTTPCredentialsIncoming(&s)
	if err != nil || got != nil {
		t.Fatalf("expected nil,nil got %v,%v", got, err)
	}
}

func TestParseHTTPCredentialsIncoming_ValidBearer(t *testing.T) {
	raw := `{"http_auth":{"mode":"bearer","bearer_token":"tok"}}`
	got, err := parseHTTPCredentialsIncoming(&raw)
	if err != nil || got == nil || got.Mode != "bearer" || got.BearerToken != "tok" {
		t.Fatalf("unexpected result: %v,%v", got, err)
	}
}

func TestParseHTTPCredentialsIncoming_InvalidJSON(t *testing.T) {
	raw := `{not json}`
	_, err := parseHTTPCredentialsIncoming(&raw)
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
}

func TestParseHTTPCredentialsIncoming_NoHTTPAuthKey(t *testing.T) {
	raw := `{"other":"value"}`
	got, err := parseHTTPCredentialsIncoming(&raw)
	if err != nil || got != nil {
		t.Fatalf("expected nil,nil for missing http_auth key: %v,%v", got, err)
	}
}

func TestApplyHTTPAuthAPIRedaction_Nil(t *testing.T) {
	// Should not panic on nil.
	ApplyHTTPAuthAPIRedaction(nil)
}

func TestApplyHTTPAuthAPIRedaction_NonHTTPService(t *testing.T) {
	s := &models.Service{Type: "tcp"}
	ApplyHTTPAuthAPIRedaction(s)
	// Non-http service: function returns early without touching fields.
	if s.HttpAuthMode != nil {
		t.Fatal("expected HttpAuthMode to be nil for non-http service")
	}
}

func TestApplyHTTPAuthAPIRedaction_NilCredentials(t *testing.T) {
	s := &models.Service{Type: "http", Credentials: nil}
	ApplyHTTPAuthAPIRedaction(s)
	if s.HttpAuthMode == nil || *s.HttpAuthMode != "none" {
		t.Fatalf("expected mode=none, got %v", s.HttpAuthMode)
	}
	if s.HttpAuthConfigured == nil || *s.HttpAuthConfigured {
		t.Fatal("expected configured=false")
	}
}

func TestApplyHTTPAuthAPIRedaction_BearerMode(t *testing.T) {
	ring := testKeyRing(t)
	plain, _ := json.Marshal(map[string]string{"token": "tok"})
	blob, _ := ring.Encrypt(plain)
	outer, _ := json.Marshal(map[string]any{
		"http_auth": httpAuthStored{Version: 1, Mode: "bearer", Secret: blob},
	})
	creds := string(outer)
	s := &models.Service{Type: "http", Credentials: &creds}
	ApplyHTTPAuthAPIRedaction(s)
	if s.Credentials != nil {
		t.Fatal("expected credentials cleared")
	}
	if s.HttpAuthMode == nil || *s.HttpAuthMode != "bearer" {
		t.Fatalf("expected mode=bearer, got %v", s.HttpAuthMode)
	}
	if s.HttpAuthConfigured == nil || !*s.HttpAuthConfigured {
		t.Fatal("expected configured=true")
	}
}

func TestApplyHTTPAuthAPIRedaction_ModeNone(t *testing.T) {
	outer, _ := json.Marshal(map[string]any{
		"http_auth": httpAuthStored{Version: 1, Mode: "none"},
	})
	creds := string(outer)
	s := &models.Service{Type: "http", Credentials: &creds}
	ApplyHTTPAuthAPIRedaction(s)
	if s.HttpAuthMode == nil || *s.HttpAuthMode != "none" {
		t.Fatalf("expected mode=none, got %v", s.HttpAuthMode)
	}
	if s.HttpAuthConfigured == nil || *s.HttpAuthConfigured {
		t.Fatal("expected configured=false for mode=none")
	}
}

func TestApplyHTTPAuthAPIRedaction_InvalidCredentialsJSON(t *testing.T) {
	creds := `{invalid}`
	s := &models.Service{Type: "http", Credentials: &creds}
	ApplyHTTPAuthAPIRedaction(s)
	// Invalid JSON → falls through to mode=none, configured=false.
	if s.HttpAuthMode == nil || *s.HttpAuthMode != "none" {
		t.Fatalf("expected mode=none on bad JSON, got %v", s.HttpAuthMode)
	}
}

func TestPrepareHTTPServiceCredentials_NilService(t *testing.T) {
	if err := PrepareHTTPServiceCredentials(nil, nil, true); err != nil {
		t.Fatalf("expected nil error for nil service, got %v", err)
	}
}

func TestPrepareHTTPServiceCredentials_NonHTTPService(t *testing.T) {
	tr := true
	s := &models.Service{Type: "tcp", PreserveHTTPSecrets: &tr}
	if err := PrepareHTTPServiceCredentials(s, nil, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.PreserveHTTPSecrets != nil {
		t.Fatal("expected PreserveHTTPSecrets cleared by sanitize")
	}
}

func TestPrepareHTTPServiceCredentials_UpdateNilCreds_NilExisting(t *testing.T) {
	s := &models.Service{Type: "http", Credentials: nil}
	if err := PrepareHTTPServiceCredentials(s, nil, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Credentials != nil {
		t.Fatal("expected nil credentials")
	}
}

func TestPrepareHTTPServiceCredentials_UpdateNilCreds_PreservesExisting(t *testing.T) {
	existCreds := `{"http_auth":{"version":1,"mode":"bearer"}}`
	s := &models.Service{Type: "http", Credentials: nil}
	existing := &models.Service{Type: "http", Credentials: &existCreds}
	if err := PrepareHTTPServiceCredentials(s, existing, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Credentials == nil || *s.Credentials != existCreds {
		t.Fatalf("expected existing credentials preserved, got %v", s.Credentials)
	}
}

func TestPrepareHTTPServiceCredentials_UpdateEmptyCreds_PreservesExisting(t *testing.T) {
	existCreds := `{"http_auth":{"version":1,"mode":"bearer"}}`
	empty := ""
	s := &models.Service{Type: "http", Credentials: &empty}
	existing := &models.Service{Type: "http", Credentials: &existCreds}
	if err := PrepareHTTPServiceCredentials(s, existing, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Credentials == nil || *s.Credentials != existCreds {
		t.Fatalf("expected existing credentials preserved, got %v", s.Credentials)
	}
}

func TestPrepareHTTPServiceCredentials_ModeNone_ClearsCredentials(t *testing.T) {
	raw := `{"http_auth":{"mode":"none"}}`
	s := &models.Service{Type: "http", Credentials: &raw}
	if err := PrepareHTTPServiceCredentials(s, nil, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Credentials != nil {
		t.Fatal("expected nil credentials for mode=none")
	}
}

func TestPrepareHTTPServiceCredentials_ModeEmpty_ClearsCredentials(t *testing.T) {
	raw := `{"http_auth":{"mode":""}}`
	s := &models.Service{Type: "http", Credentials: &raw}
	if err := PrepareHTTPServiceCredentials(s, nil, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Credentials != nil {
		t.Fatal("expected nil credentials for empty mode")
	}
}

func TestPrepareHTTPServiceCredentials_UnknownMode_Error(t *testing.T) {
	raw := `{"http_auth":{"mode":"oauth2"}}`
	s := &models.Service{Type: "http", Credentials: &raw}
	if err := PrepareHTTPServiceCredentials(s, nil, true); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestPrepareHTTPServiceCredentials_BearerToken_Success(t *testing.T) {
	raw := `{"http_auth":{"mode":"bearer","bearer_token":"my-secret-token"}}`
	s := &models.Service{Type: "http", Credentials: &raw}
	if err := PrepareHTTPServiceCredentials(s, nil, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Credentials == nil {
		t.Fatal("expected encrypted credentials to be set")
	}
	// Verify the stored credentials contain encrypted http_auth.
	var outer struct {
		HTTPAuth *httpAuthStored `json:"http_auth"`
	}
	if err := json.Unmarshal([]byte(*s.Credentials), &outer); err != nil {
		t.Fatalf("credentials not valid JSON: %v", err)
	}
	if outer.HTTPAuth == nil || outer.HTTPAuth.Mode != "bearer" || outer.HTTPAuth.Secret == nil {
		t.Fatalf("unexpected stored form: %+v", outer.HTTPAuth)
	}
}

func TestPrepareHTTPServiceCredentials_BearerToken_EmptyError(t *testing.T) {
	raw := `{"http_auth":{"mode":"bearer","bearer_token":""}}`
	s := &models.Service{Type: "http", Credentials: &raw}
	if err := PrepareHTTPServiceCredentials(s, nil, true); err == nil {
		t.Fatal("expected error for empty bearer token")
	}
}

func TestPrepareHTTPServiceCredentials_BearerPreserve_Success(t *testing.T) {
	// Build encrypted existing credentials with bearer token "old-token".
	ring := testKeyRing(t)
	plain, _ := json.Marshal(map[string]string{"token": "old-token"})
	blob, _ := ring.Encrypt(plain)
	storedJSON, _ := json.Marshal(map[string]any{
		"http_auth": httpAuthStored{Version: 1, Mode: "bearer", Secret: blob},
	})
	existCreds := string(storedJSON)
	existing := &models.Service{Type: "http", Credentials: &existCreds}

	// Update with empty bearer_token + preserve flag → should reuse old-token.
	tr := true
	raw := `{"http_auth":{"mode":"bearer","bearer_token":""}}`
	s := &models.Service{Type: "http", Credentials: &raw, PreserveHTTPSecrets: &tr}
	if err := PrepareHTTPServiceCredentials(s, existing, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Credentials == nil {
		t.Fatal("expected credentials to be set after preserve")
	}
}

func TestPrepareHTTPServiceCredentials_BearerPreserve_DecryptError(t *testing.T) {
	// Existing credentials reference key "v999" which is not in the Global() ring
	// (TestMain only configures "v1"), so Decrypt returns a "key not available" error.
	badBlob := &credentialenc.Blob{
		KID: "v999",
		N:   "AAAAAAAAAAAAAAAAAAAAAA==", // valid base64, never reached
		D:   "AAAA",
	}
	outer, _ := json.Marshal(map[string]any{
		"http_auth": httpAuthStored{Version: 1, Mode: "bearer", Secret: badBlob},
	})
	badCreds := string(outer)
	existing := &models.Service{Type: "http", Credentials: &badCreds}

	tr := true
	raw := `{"http_auth":{"mode":"bearer","bearer_token":""}}`
	s := &models.Service{Type: "http", Credentials: &raw, PreserveHTTPSecrets: &tr}
	if err := PrepareHTTPServiceCredentials(s, existing, false); err == nil {
		t.Fatal("expected decrypt error for bad blob")
	}
}

func TestPrepareHTTPServiceCredentials_Basic_Success(t *testing.T) {
	raw := `{"http_auth":{"mode":"basic","basic_user":"user","basic_password":"pass"}}`
	s := &models.Service{Type: "http", Credentials: &raw}
	if err := PrepareHTTPServiceCredentials(s, nil, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Credentials == nil {
		t.Fatal("expected encrypted credentials for basic")
	}
}

func TestPrepareHTTPServiceCredentials_Basic_EmptyUserError(t *testing.T) {
	raw := `{"http_auth":{"mode":"basic","basic_user":"","basic_password":"pass"}}`
	s := &models.Service{Type: "http", Credentials: &raw}
	if err := PrepareHTTPServiceCredentials(s, nil, true); err == nil {
		t.Fatal("expected error for empty basic user")
	}
}

func TestPrepareHTTPServiceCredentials_BasicPreserve_Success(t *testing.T) {
	ring := testKeyRing(t)
	plain, _ := json.Marshal(map[string]string{"user": "olduser", "password": "oldpass"})
	blob, _ := ring.Encrypt(plain)
	storedJSON, _ := json.Marshal(map[string]any{
		"http_auth": httpAuthStored{Version: 1, Mode: "basic", Secret: blob},
	})
	existCreds := string(storedJSON)
	existing := &models.Service{Type: "http", Credentials: &existCreds}

	tr := true
	// Empty user+pass with preserve → should reuse old values.
	raw := `{"http_auth":{"mode":"basic","basic_user":"","basic_password":""}}`
	s := &models.Service{Type: "http", Credentials: &raw, PreserveHTTPSecrets: &tr}
	if err := PrepareHTTPServiceCredentials(s, existing, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Credentials == nil {
		t.Fatal("expected credentials set after basic preserve")
	}
}

func TestPrepareHTTPServiceCredentials_InvalidCredentialsJSON(t *testing.T) {
	raw := `{not json}`
	s := &models.Service{Type: "http", Credentials: &raw}
	if err := PrepareHTTPServiceCredentials(s, nil, true); err == nil {
		t.Fatal("expected JSON parse error")
	}
}

func TestDecryptHTTPAuthForWorker_NilCredentials(t *testing.T) {
	result, err := DecryptHTTPAuthForWorker(nil, nil)
	if err != nil || result == nil || result.Mode != "none" {
		t.Fatalf("expected {Mode:none}, got %v/%v", result, err)
	}
}

func TestDecryptHTTPAuthForWorker_EmptyCredentials(t *testing.T) {
	empty := ""
	result, err := DecryptHTTPAuthForWorker(&empty, nil)
	if err != nil || result == nil || result.Mode != "none" {
		t.Fatalf("expected {Mode:none}, got %v/%v", result, err)
	}
}

func TestDecryptHTTPAuthForWorker_NilRing_UsesGlobal(t *testing.T) {
	// Credentials with mode=none → no decryption needed; nil ring path still works.
	creds := `{"http_auth":{"version":1,"mode":"none"}}`
	result, err := DecryptHTTPAuthForWorker(&creds, nil)
	if err != nil || result == nil || result.Mode != "none" {
		t.Fatalf("expected {Mode:none}, got %v/%v", result, err)
	}
}

func TestDecryptHTTPAuthForWorker_Bearer(t *testing.T) {
	ring := testKeyRing(t)
	plain, _ := json.Marshal(map[string]string{"token": "my-bearer"})
	blob, _ := ring.Encrypt(plain)
	outer, _ := json.Marshal(map[string]any{
		"http_auth": httpAuthStored{Version: 1, Mode: "bearer", Secret: blob},
	})
	creds := string(outer)
	result, err := DecryptHTTPAuthForWorker(&creds, ring)
	if err != nil || result.Mode != "bearer" || result.BearerToken != "my-bearer" {
		t.Fatalf("unexpected result: %v/%v", result, err)
	}
}

func TestDecryptHTTPAuthForWorker_Basic(t *testing.T) {
	ring := testKeyRing(t)
	plain, _ := json.Marshal(map[string]string{"user": "admin", "password": "s3cr3t"})
	blob, _ := ring.Encrypt(plain)
	outer, _ := json.Marshal(map[string]any{
		"http_auth": httpAuthStored{Version: 1, Mode: "basic", Secret: blob},
	})
	creds := string(outer)
	result, err := DecryptHTTPAuthForWorker(&creds, ring)
	if err != nil || result.Mode != "basic" || result.BasicUser != "admin" || result.BasicPassword != "s3cr3t" {
		t.Fatalf("unexpected result: %v/%v", result, err)
	}
}

func TestDecryptHTTPAuthForWorker_UnknownMode_Error(t *testing.T) {
	ring := testKeyRing(t)
	plain, _ := json.Marshal(map[string]string{"x": "y"})
	blob, _ := ring.Encrypt(plain)
	outer, _ := json.Marshal(map[string]any{
		"http_auth": httpAuthStored{Version: 1, Mode: "oauth", Secret: blob},
	})
	creds := string(outer)
	_, err := DecryptHTTPAuthForWorker(&creds, ring)
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestDecryptHTTPAuthForWorker_InvalidJSON(t *testing.T) {
	creds := `{invalid}`
	_, err := DecryptHTTPAuthForWorker(&creds, testKeyRing(t))
	if err == nil {
		t.Fatal("expected JSON error")
	}
}

func TestDecryptHTTPAuthForWorker_MissingBlob(t *testing.T) {
	// mode is bearer but secret blob is absent → error "missing encrypted secret blob"
	outer, _ := json.Marshal(map[string]any{
		"http_auth": httpAuthStored{Version: 1, Mode: "bearer", Secret: nil},
	})
	creds := string(outer)
	_, err := DecryptHTTPAuthForWorker(&creds, testKeyRing(t))
	if err == nil {
		t.Fatal("expected error for missing blob")
	}
}

func TestDecryptHTTPAuthForWorker_NilHTTPAuth_ReturnsNone(t *testing.T) {
	// Valid JSON but http_auth field is null → treated as mode=none.
	outer, _ := json.Marshal(map[string]any{"http_auth": nil})
	creds := string(outer)
	result, err := DecryptHTTPAuthForWorker(&creds, testKeyRing(t))
	if err != nil || result == nil || result.Mode != "none" {
		t.Fatalf("expected {Mode:none}, got %v/%v", result, err)
	}
}
