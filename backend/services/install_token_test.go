package services

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestNewInstallTokenService(t *testing.T) {
	db, _ := newDB(t)
	if NewInstallTokenService(db) == nil {
		t.Fatal("expected non-nil")
	}
}

func TestInstallTokenService_ValidateToken_EmptyToken(t *testing.T) {
	db, _ := newDB(t)
	svc := NewInstallTokenService(db)
	_, err := svc.ValidateToken("")
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("expected required error, got %v", err)
	}
}

func TestInstallTokenService_ValidateToken_NotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewInstallTokenService(db)
	mock.ExpectQuery("SELECT organization_id").WillReturnError(sql.ErrNoRows)
	_, err := svc.ValidateToken("abc123")
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("expected invalid error, got %v", err)
	}
}

func TestInstallTokenService_ValidateToken_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewInstallTokenService(db)
	mock.ExpectQuery("SELECT organization_id").WillReturnError(sql.ErrConnDone)
	_, err := svc.ValidateToken("abc123")
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestInstallTokenService_ValidateToken_Expired(t *testing.T) {
	db, mock := newDB(t)
	svc := NewInstallTokenService(db)
	expired := sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true}
	mock.ExpectQuery("SELECT organization_id").
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "expires_at"}).AddRow(1, expired))
	_, err := svc.ValidateToken("abc123")
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired error, got %v", err)
	}
}

func TestInstallTokenService_ValidateToken_Valid(t *testing.T) {
	db, mock := newDB(t)
	svc := NewInstallTokenService(db)
	future := sql.NullTime{Time: time.Now().Add(time.Hour), Valid: true}
	mock.ExpectQuery("SELECT organization_id").
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "expires_at"}).AddRow(5, future))
	orgID, err := svc.ValidateToken("abc123")
	if err != nil || orgID != 5 {
		t.Fatalf("expected orgID=5, got %d/%v", orgID, err)
	}
}

func TestInstallTokenService_ValidateToken_NoExpiry(t *testing.T) {
	db, mock := newDB(t)
	svc := NewInstallTokenService(db)
	mock.ExpectQuery("SELECT organization_id").
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "expires_at"}).AddRow(3, sql.NullTime{Valid: false}))
	orgID, err := svc.ValidateToken("abc123")
	if err != nil || orgID != 3 {
		t.Fatalf("expected orgID=3, got %d/%v", orgID, err)
	}
}

func TestInstallTokenService_CreateToken_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewInstallTokenService(db)
	now := time.Now()
	createdBy := int64(1)
	mock.ExpectQuery("INSERT INTO install_tokens").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "organization_id", "token", "name", "created_by", "created_at", "expires_at",
		}).AddRow(1, 1, "abc123def456ghij", sql.NullString{String: "deploy", Valid: true},
			sql.NullInt64{Int64: createdBy, Valid: true}, now, sql.NullTime{Valid: false}))
	token, err := svc.CreateToken(1, "deploy", nil, &createdBy)
	if err != nil || token.ID != 1 {
		t.Fatalf("expected created token: %v/%v", token, err)
	}
	if token.Name != "deploy" {
		t.Fatalf("expected name=deploy, got %q", token.Name)
	}
}

func TestInstallTokenService_CreateToken_WithExpiry(t *testing.T) {
	db, mock := newDB(t)
	svc := NewInstallTokenService(db)
	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)
	mock.ExpectQuery("INSERT INTO install_tokens").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "organization_id", "token", "name", "created_by", "created_at", "expires_at",
		}).AddRow(2, 1, "xyz987", sql.NullString{Valid: false},
			sql.NullInt64{Valid: false}, now, sql.NullTime{Time: expiresAt, Valid: true}))
	token, err := svc.CreateToken(1, "", &expiresAt, nil)
	if err != nil || token.ExpiresAt == nil {
		t.Fatalf("expected token with expiry: %v/%v", token, err)
	}
}

func TestInstallTokenService_CreateToken_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewInstallTokenService(db)
	mock.ExpectQuery("INSERT INTO install_tokens").WillReturnError(sql.ErrConnDone)
	_, err := svc.CreateToken(1, "", nil, nil)
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestInstallTokenService_ListTokens_Empty(t *testing.T) {
	db, mock := newDB(t)
	svc := NewInstallTokenService(db)
	mock.ExpectQuery("SELECT id").WillReturnRows(sqlmock.NewRows([]string{
		"id", "organization_id", "token", "name", "created_by", "created_at", "expires_at",
	}))
	tokens, err := svc.ListTokens(1)
	if err != nil || len(tokens) != 0 {
		t.Fatalf("expected empty: %v/%v", tokens, err)
	}
}

func TestInstallTokenService_ListTokens_WithResults(t *testing.T) {
	db, mock := newDB(t)
	svc := NewInstallTokenService(db)
	now := time.Now()
	mock.ExpectQuery("SELECT id").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "organization_id", "token", "name", "created_by", "created_at", "expires_at",
		}).AddRow(1, 1, "longtokenvalue12345678", sql.NullString{Valid: false},
			sql.NullInt64{Valid: false}, now, sql.NullTime{Valid: false}))
	tokens, err := svc.ListTokens(1)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("expected 1 token: %v/%v", tokens, err)
	}
	if tokens[0].TokenPrefix != "longtoke" { // first 8 chars
		t.Fatalf("expected prefix 'longtoke', got %q", tokens[0].TokenPrefix)
	}
}

func TestInstallTokenService_ListTokens_ShortToken(t *testing.T) {
	db, mock := newDB(t)
	svc := NewInstallTokenService(db)
	now := time.Now()
	mock.ExpectQuery("SELECT id").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "organization_id", "token", "name", "created_by", "created_at", "expires_at",
		}).AddRow(1, 1, "abc", sql.NullString{Valid: false},
			sql.NullInt64{Valid: false}, now, sql.NullTime{Valid: false}))
	tokens, err := svc.ListTokens(1)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("expected 1 token: %v/%v", tokens, err)
	}
	if tokens[0].TokenPrefix != "abc" {
		t.Fatalf("expected prefix 'abc' for short token, got %q", tokens[0].TokenPrefix)
	}
}

func TestInstallTokenService_ListTokens_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewInstallTokenService(db)
	mock.ExpectQuery("SELECT id").WillReturnError(sql.ErrConnDone)
	_, err := svc.ListTokens(1)
	if err == nil {
		t.Fatal("expected DB error")
	}
}

func TestInstallTokenService_DeleteToken_Success(t *testing.T) {
	db, mock := newDB(t)
	svc := NewInstallTokenService(db)
	mock.ExpectExec("DELETE FROM install_tokens").WillReturnResult(sqlmock.NewResult(1, 1))
	if err := svc.DeleteToken(1, 1); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestInstallTokenService_DeleteToken_NotFound(t *testing.T) {
	db, mock := newDB(t)
	svc := NewInstallTokenService(db)
	mock.ExpectExec("DELETE FROM install_tokens").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := svc.DeleteToken(1, 1); err == nil {
		t.Fatal("expected not found error")
	}
}

func TestInstallTokenService_DeleteToken_DBError(t *testing.T) {
	db, mock := newDB(t)
	svc := NewInstallTokenService(db)
	mock.ExpectExec("DELETE FROM install_tokens").WillReturnError(sql.ErrConnDone)
	if err := svc.DeleteToken(1, 1); err == nil {
		t.Fatal("expected DB error")
	}
}
