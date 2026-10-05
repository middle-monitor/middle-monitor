package services

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestFrontendBaseURL_Empty(t *testing.T) {
	t.Setenv("FRONTEND_URL", "")
	if got := frontendBaseURL(); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestFrontendBaseURL_Single(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://app.example.com/")
	if got := frontendBaseURL(); got != "https://app.example.com" {
		t.Fatalf("want without trailing slash, got %q", got)
	}
}

func TestFrontendBaseURL_Multiple(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://app.example.com, https://other.example.com")
	if got := frontendBaseURL(); got != "https://app.example.com" {
		t.Fatalf("want first URL, got %q", got)
	}
}

func TestOrgSlug_Found(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectQuery("SELECT slug FROM organizations").
		WillReturnRows(sqlmock.NewRows([]string{"slug"}).AddRow("myorg"))
	slug := orgSlug(db, 1)
	if slug != "myorg" {
		t.Fatalf("want myorg, got %q", slug)
	}
}

func TestOrgSlug_NotFound(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectQuery("SELECT slug FROM organizations").WillReturnError(sql.ErrNoRows)
	slug := orgSlug(db, 1)
	if slug != "" {
		t.Fatalf("want empty, got %q", slug)
	}
}

func TestServiceURL_NoFrontendURL(t *testing.T) {
	t.Setenv("FRONTEND_URL", "")
	db, _ := newDB(t)
	url := serviceURL(db, 1, 2)
	if url != "" {
		t.Fatalf("want empty, got %q", url)
	}
}

func TestServiceURL_NoSlug(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://app.example.com")
	db, mock := newDB(t)
	mock.ExpectQuery("SELECT slug FROM organizations").WillReturnError(sql.ErrNoRows)
	url := serviceURL(db, 1, 2)
	if url != "" {
		t.Fatalf("want empty for missing slug, got %q", url)
	}
}

func TestServiceURL_Full(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://app.example.com")
	db, mock := newDB(t)
	mock.ExpectQuery("SELECT slug FROM organizations").
		WillReturnRows(sqlmock.NewRows([]string{"slug"}).AddRow("myorg"))
	url := serviceURL(db, 1, 42)
	if url != "https://app.example.com/organizations/myorg/services/42" {
		t.Fatalf("unexpected URL: %q", url)
	}
}

func TestServiceAlertContext_DBError(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectQuery("SELECT s.name").WillReturnError(sql.ErrNoRows)
	ctx := ServiceAlertContext(db, 1)
	if ctx != "" {
		t.Fatalf("want empty on error, got %q", ctx)
	}
}

func TestServiceAlertContext_Full(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://app.example.com")
	db, mock := newDB(t)
	mock.ExpectQuery("SELECT s.name").
		WillReturnRows(sqlmock.NewRows([]string{"name", "org_id", "host_name"}).
			AddRow("api", 1, sql.NullString{String: "server1", Valid: true}))
	mock.ExpectQuery("SELECT slug FROM organizations").
		WillReturnRows(sqlmock.NewRows([]string{"slug"}).AddRow("myorg"))

	ctx := ServiceAlertContext(db, 1)
	if !strings.Contains(ctx, "api") {
		t.Fatalf("expected service name in context: %q", ctx)
	}
	if !strings.Contains(ctx, "server1") {
		t.Fatalf("expected host name in context: %q", ctx)
	}
}

func TestServiceAlertContext_EmptyName(t *testing.T) {
	t.Setenv("FRONTEND_URL", "")
	db, mock := newDB(t)
	mock.ExpectQuery("SELECT s.name").
		WillReturnRows(sqlmock.NewRows([]string{"name", "org_id", "host_name"}).
			AddRow("", 1, sql.NullString{Valid: false}))

	ctx := ServiceAlertContext(db, 1)
	// Empty name/env/host → empty context (no-frontend-url → no link)
	if ctx != "" {
		t.Fatalf("want empty context, got %q", ctx)
	}
}

func TestHostAlertContext_DBError(t *testing.T) {
	db, mock := newDB(t)
	mock.ExpectQuery("SELECT name").WillReturnError(sql.ErrNoRows)
	ctx := HostAlertContext(db, 1)
	if ctx != "" {
		t.Fatalf("want empty on error, got %q", ctx)
	}
}

func TestHostAlertContext_NoFrontendURL(t *testing.T) {
	t.Setenv("FRONTEND_URL", "")
	db, mock := newDB(t)
	mock.ExpectQuery("SELECT name").
		WillReturnRows(sqlmock.NewRows([]string{"name", "org_id"}).AddRow("host1", 1))

	ctx := HostAlertContext(db, 1)
	if !strings.Contains(ctx, "host1") {
		t.Fatalf("expected host name in context: %q", ctx)
	}
	if strings.Contains(ctx, "http") {
		t.Fatalf("should not contain URL without FRONTEND_URL: %q", ctx)
	}
}

func TestHostAlertContext_WithURL(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://app.example.com")
	db, mock := newDB(t)
	mock.ExpectQuery("SELECT name").
		WillReturnRows(sqlmock.NewRows([]string{"name", "org_id"}).AddRow("host1", 1))
	mock.ExpectQuery("SELECT slug FROM organizations").
		WillReturnRows(sqlmock.NewRows([]string{"slug"}).AddRow("myorg"))

	ctx := HostAlertContext(db, 1)
	if !strings.Contains(ctx, "hosts/1") {
		t.Fatalf("expected host URL in context: %q", ctx)
	}
}

func TestHostAlertContext_EmptyName(t *testing.T) {
	t.Setenv("FRONTEND_URL", "")
	db, mock := newDB(t)
	mock.ExpectQuery("SELECT name").
		WillReturnRows(sqlmock.NewRows([]string{"name", "org_id"}).AddRow("", 1))
	ctx := HostAlertContext(db, 1)
	if ctx != "" {
		t.Fatalf("want empty for empty name/env, got %q", ctx)
	}
}

func TestHostAlertContext_SlugNotFound(t *testing.T) {
	t.Setenv("FRONTEND_URL", "https://app.example.com")
	db, mock := newDB(t)
	mock.ExpectQuery("SELECT name").
		WillReturnRows(sqlmock.NewRows([]string{"name", "org_id"}).AddRow("h", 1))
	mock.ExpectQuery("SELECT slug FROM organizations").WillReturnError(sql.ErrNoRows)
	ctx := HostAlertContext(db, 1)
	if strings.Contains(ctx, "organizations") {
		t.Fatalf("should not have link when slug not found: %q", ctx)
	}
}
