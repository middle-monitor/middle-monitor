package services

import (
	"testing"
)

func TestEncryptSQLCredentials_Empty(t *testing.T) {
	result, err := EncryptSQLCredentials("")
	if err != nil || result != "" {
		t.Fatalf("expected empty, got %q/%v", result, err)
	}
}

func TestEncryptSQLCredentials_NonSQLFormat(t *testing.T) {
	result, err := EncryptSQLCredentials(`{"key":"value"}`)
	if err != nil {
		t.Fatalf("expected no error for non-SQL format, got %v", err)
	}
	// Non-SQL format → returned unchanged
	_ = result
}

func TestEncryptSQLCredentials_NoPassword(t *testing.T) {
	creds := `{"host":"localhost","port":"5432","database":"mydb"}`
	result, err := EncryptSQLCredentials(creds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = result
}

func TestEncryptSQLCredentials_WithPasswordNoKeyRing(t *testing.T) {
	// Without encryption keys configured, returns original JSON
	t.Setenv("MIDDLE_MONITOR_KEYS", "")
	t.Setenv("MIDDLE_MONITOR_KEY", "")
	t.Setenv("MIDDLE_MONITOR_ACTIVE_KEY_ID", "")
	creds := `{"host":"localhost","port":"5432","database":"mydb","username":"admin","password":"secret"}`
	result, err := EncryptSQLCredentials(creds)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = result
}

func TestDecryptSQLPassword_Empty(t *testing.T) {
	pwd, err := DecryptSQLPassword("")
	if err != nil || pwd != "" {
		t.Fatalf("expected empty, got %q/%v", pwd, err)
	}
}

func TestDecryptSQLPassword_InvalidJSON(t *testing.T) {
	_, err := DecryptSQLPassword("not json")
	if err == nil {
		t.Fatal("expected JSON error")
	}
}

func TestDecryptSQLPassword_LegacyPlaintext(t *testing.T) {
	creds := `{"host":"localhost","port":"5432","database":"mydb","password":"plaintext"}`
	pwd, err := DecryptSQLPassword(creds)
	if err != nil || pwd != "plaintext" {
		t.Fatalf("expected plaintext, got %q/%v", pwd, err)
	}
}

func TestDecryptSQLPassword_LegacyNoPassword(t *testing.T) {
	creds := `{"host":"localhost","port":"5432","database":"mydb"}`
	pwd, err := DecryptSQLPassword(creds)
	if err != nil || pwd != "" {
		t.Fatalf("expected empty password, got %q/%v", pwd, err)
	}
}
