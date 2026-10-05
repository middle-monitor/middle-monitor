package services

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestNewAPIKeyService(t *testing.T) {
	db, _ := newDB(t)
	if NewAPIKeyService(db) == nil {
		t.Fatal("expected non-nil")
	}
}

func TestAPIKeyService_CreateKey_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAPIKeyService(db)
	now := time.Now()
	mock.ExpectQuery("INSERT INTO api_keys").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, now))
	key, err := svc.CreateKey(1, "CI Key", "read", nil, nil)
	if err != nil || key.ID != 1 {
		t.Fatalf("expected created key: %v/%v", key, err)
	}
	if !strings.HasPrefix(key.Key, "mm_") {
		t.Fatal("key should start with mm_")
	}
}

func TestAPIKeyService_CreateKey_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAPIKeyService(db)
	mock.ExpectQuery("INSERT INTO api_keys").WillReturnError(sql.ErrConnDone)
	_, err := svc.CreateKey(1, "key", "read", nil, nil)
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestAPIKeyService_ValidateKey_NotAPIKey(t *testing.T) {
	db, _ := newDB(t)
	svc := NewAPIKeyService(db)
	_, err := svc.ValidateKey("not-an-api-key")
	if err == nil || !strings.Contains(err.Error(), "not an api key") {
		t.Fatalf("expected not api key error, got %v", err)
	}
}

func TestAPIKeyService_ValidateKey_InvalidHash(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAPIKeyService(db)
	mock.ExpectQuery("SELECT k.id").WillReturnError(sql.ErrNoRows)
	_, err := svc.ValidateKey("mm_invalidkey")
	if err == nil || !strings.Contains(err.Error(), "invalid api key") {
		t.Fatalf("expected invalid api key error, got %v", err)
	}
}

func TestAPIKeyService_ValidateKey_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAPIKeyService(db)
	mock.ExpectQuery("SELECT k.id").WillReturnError(sql.ErrConnDone)
	_, err := svc.ValidateKey("mm_somekey")
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestAPIKeyService_ValidateKey_Expired(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAPIKeyService(db)
	expired := sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true}
	mock.ExpectQuery("SELECT k.id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "organization_id", "slug", "expires_at", "user_id", "role", "email"}).
			AddRow(1, 1, "org", expired, nil, nil, nil))
	_, err := svc.ValidateKey("mm_testkey")
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired error, got %v", err)
	}
}

func TestAPIKeyService_ValidateKey_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAPIKeyService(db)
	mock.ExpectQuery("SELECT k.id").WillReturnRows(
		sqlmock.NewRows([]string{"id", "organization_id", "slug", "expires_at", "user_id", "role", "email"}).
			AddRow(1, 10, "myorg", sql.NullTime{Valid: false}, nil, nil, nil))
	mock.ExpectExec("UPDATE api_keys").WillReturnResult(sqlmock.NewResult(1, 1))
	ident, err := svc.ValidateKey("mm_testkey")
	if err != nil || ident.OrgID != 10 || ident.OrgSlug != "myorg" || ident.UserID != nil {
		t.Fatalf("expected org token orgID=10/slug=myorg/no user, got %+v/%v", ident, err)
	}
}

func TestAPIKeyService_GetKeys_Empty(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAPIKeyService(db)
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows([]string{
		"id", "organization_id", "name", "key_prefix", "scopes", "last_used_at", "expires_at", "created_by", "created_at",
	}))
	keys, err := svc.GetKeys(1)
	if err != nil || len(keys) != 0 {
		t.Fatalf("expected empty: %v/%v", keys, err)
	}
}

func TestAPIKeyService_GetKeys_WithResult(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAPIKeyService(db)
	now := time.Now()
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows([]string{
		"id", "organization_id", "name", "key_prefix", "scopes", "last_used_at", "expires_at", "created_by", "created_at",
	}).AddRow(1, 1, "CI Key", "mm_12345", "read", nil, nil, nil, now))
	keys, err := svc.GetKeys(1)
	if err != nil || len(keys) != 1 {
		t.Fatalf("expected 1 key: %v/%v", keys, err)
	}
}

func TestAPIKeyService_GetKeys_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAPIKeyService(db)
	mock.ExpectQuery("SELECT id").WillReturnError(sql.ErrConnDone)
	_, err := svc.GetKeys(1)
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestAPIKeyService_DeleteKey_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAPIKeyService(db)
	mock.ExpectExec("DELETE FROM api_keys").
		WithArgs(int64(7), int64(1)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := svc.DeleteKey(7, 1); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestAPIKeyService_DeleteKey_NotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAPIKeyService(db)
	mock.ExpectExec("DELETE FROM api_keys").
		WillReturnResult(sqlmock.NewResult(0, 0))
	err := svc.DeleteKey(99, 1)
	if err == nil {
		t.Fatal("expected error for not-found key")
	}
}

func TestAPIKeyService_DeleteKey_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewAPIKeyService(db)
	mock.ExpectExec("DELETE FROM api_keys").
		WillReturnError(errors.New("db error"))
	if err := svc.DeleteKey(1, 1); err == nil {
		t.Fatal("expected error")
	}
}
