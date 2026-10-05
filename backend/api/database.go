package api

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/golang-migrate/migrate/v4"
	pgmigrate "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func buildDSN() string {
	host := os.Getenv("DB_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("DB_PORT")
	if port == "" {
		port = "5432"
	}
	user := os.Getenv("DB_USER")
	if user == "" {
		user = "postgres"
	}
	password := os.Getenv("DB_PASSWORD")
	if password == "" {
		password = "postgres"
	}
	dbname := os.Getenv("DB_NAME")
	if dbname == "" {
		dbname = "middlemonitor"
	}

	// SSL mode is configurable so we can enable verify-full in production.
	// Defaults to "disable" so local dev keeps working without changes.
	sslmode := os.Getenv("DB_SSLMODE")
	if sslmode == "" {
		sslmode = "disable"
	}

	// Add timezone=UTC to DSN to ensure PostgreSQL handles timestamps in UTC
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s timezone=UTC",
		host, port, user, password, dbname, sslmode)
}

func InitDB() (*sql.DB, error) {
	db, err := sql.Open("postgres", buildDSN())
	if err != nil {
		return nil, err
	}

	// Connection pool: required to avoid exhausting Postgres at moderate load.
	// Tunable via DB_MAX_OPEN_CONNS / DB_MAX_IDLE_CONNS / DB_CONN_MAX_LIFETIME_MIN.
	db.SetMaxOpenConns(envInt("DB_MAX_OPEN_CONNS", 25))
	db.SetMaxIdleConns(envInt("DB_MAX_IDLE_CONNS", 10))
	db.SetConnMaxLifetime(time.Duration(envInt("DB_CONN_MAX_LIFETIME_MIN", 30)) * time.Minute)
	db.SetConnMaxIdleTime(time.Duration(envInt("DB_CONN_MAX_IDLE_MIN", 10)) * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, err
	}

	return db, nil
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

// RunMigrations applies the versioned SQL migrations embedded in the binary
// (api/migrations/*.sql). It runs at every boot: golang-migrate compares the
// schema_migrations table against the embedded files and applies only the
// pending ones, under a Postgres advisory lock so concurrent instances can't
// race each other.
//
// To add a migration, drop a new file in api/migrations/ following the
// NNNNNN_name.up.sql convention (strictly increasing prefix) and rebuild.
// Databases created by the legacy inline migrations are adopted transparently:
// the baseline (000001) is fully idempotent, so it applies as a no-op and the
// database gets stamped at version 1.
func RunMigrations(db *sql.DB) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrEmbeddedMigrationsLoad, err)
	}
	// The migrate driver takes ownership of the *sql.DB it is handed and closes
	// it in Close() — even via WithInstance. Give it a private connection so
	// the caller's pool survives the migration run.
	migDB, err := sql.Open("postgres", buildDSN())
	if err != nil {
		return fmt.Errorf("%w: %w", ErrMigrationConnectionOpen, err)
	}
	driver, err := pgmigrate.WithInstance(migDB, &pgmigrate.Config{})
	if err != nil {
		migDB.Close()
		return fmt.Errorf("%w: %w", ErrMigrationDriverInitialize, err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		migDB.Close()
		return fmt.Errorf("%w: %w", ErrMigratorInitialize, err)
	}

	upErr := m.Up()
	version, dirty, verErr := m.Version()

	if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
		slog.Warn("migration close warning", "source_error", srcErr, "database_error", dbErr)
	}

	if upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		if dirty {
			// A migration failed midway: schema_migrations is flagged dirty and
			// every subsequent boot will refuse to run until an operator fixes
			// the schema and clears the flag.
			return &MigrationDirtyError{
				Version: int(version),
				Hint:    "fix the database then run: UPDATE schema_migrations SET dirty = false",
				Err:     upErr,
			}
		}
		return fmt.Errorf("%w: %w", ErrMigrationsApply, upErr)
	}

	switch {
	case verErr != nil:
		slog.Warn("schema version unknown", "error", verErr)
	case errors.Is(upErr, migrate.ErrNoChange):
		slog.Info("schema up to date", "version", version)
	default:
		slog.Info("schema migrated", "version", version)
	}

	// Seed the opt-in bootstrap admin (SEED_ADMIN_* env vars)
	if err := seedDefaultAdmin(db); err != nil {
		return fmt.Errorf("%w: %w", ErrDefaultAdminSeed, err)
	}

	// Seed the opt-in self-monitoring checks (SELF_MONITOR_ORG_SLUG env var).
	// Failures are logged, never fatal: monitoring ourselves must not stop the boot.
	ensureSelfMonitorChecks(db)

	return nil
}

// seedDefaultAdmin creates a bootstrap admin user when the caller explicitly
// opted in via SEED_ADMIN_EMAIL + SEED_ADMIN_PASSWORD.
//
// The guard is per-email, not "any user exists": on an instance that already
// has registered users the seeded operator account must still converge,
// otherwise setting the env vars after the first signup silently does nothing.
// The target org (SEED_ADMIN_ORG_SLUG, default "default") is created if
// missing and pinned to the "pro" plan — the operator org dogfoods the same
// limits a paying customer gets, instead of being exempt via "enterprise".
// There is deliberately no default account: a well-known credential is the
// first thing scanners try.
func seedDefaultAdmin(db *sql.DB) error {
	email := os.Getenv("SEED_ADMIN_EMAIL")
	password := os.Getenv("SEED_ADMIN_PASSWORD")
	if email == "" || password == "" {
		// No-op by design when the env vars aren't set.
		return nil
	}

	slug := os.Getenv("SEED_ADMIN_ORG_SLUG")
	if slug == "" {
		slug = "default"
	}

	var orgID int64
	err := db.QueryRow(`SELECT id FROM organizations WHERE slug = $1`, slug).Scan(&orgID)
	if err == sql.ErrNoRows {
		err = db.QueryRow(`
			INSERT INTO organizations (name, slug, plan, created_at, updated_at)
			VALUES ($1, $1, 'pro', NOW(), NOW())
			RETURNING id
		`, slug).Scan(&orgID)
		if err == nil {
			slog.Info("organization created for seeded admin", "slug", slug)
		}
	}
	if err != nil {
		return fmt.Errorf("seed admin organization %q: %w", slug, err)
	}

	// Pin the operator org to "pro" so it dogfoods real customer limits.
	// Re-applied at every boot so the plan converges even if it was changed
	// (in the DB, or by a stray Stripe webhook) since the last boot.
	if _, err := db.Exec(`UPDATE organizations SET plan = 'pro', updated_at = NOW() WHERE id = $1 AND plan <> 'pro'`, orgID); err != nil {
		return fmt.Errorf("%w: %w", ErrAdminOrgPlanSeed, err)
	}

	// SEED_ADMIN_* declare the desired credentials for the bootstrap account:
	// when the user already exists, the password must CONVERGE to the env
	// value, otherwise editing SEED_ADMIN_PASSWORD after the first boot leaves
	// a stale hash in the DB and locks the operator out with a 401 (there is
	// no other reset path for this account). Corollary: while the env vars
	// stay set, a password change made in the UI is reverted at the next boot.
	var currentHash string
	err = db.QueryRow(`SELECT password_hash FROM users WHERE email = $1`, email).Scan(&currentHash)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	userExists := err == nil

	if userExists && bcrypt.CompareHashAndPassword([]byte(currentHash), []byte(password)) == nil {
		slog.Info("seed admin already matches env vars", "email", email)
		return seedAdminMembership(db, email)
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPasswordHash, err)
	}

	if userExists {
		if _, err := db.Exec(`UPDATE users SET password_hash = $1, email_verified = true, updated_at = NOW() WHERE email = $2`, string(hashedPassword), email); err != nil {
			return fmt.Errorf("%w: %w", ErrSeededAdminPasswordConverge, err)
		}
		slog.Info("seed admin password converged", "email", email)
		return seedAdminMembership(db, email)
	}

	name := os.Getenv("SEED_ADMIN_NAME")
	if name == "" {
		name = "Admin"
	}

	_, err = db.Exec(`
		INSERT INTO users (organization_id, email, password_hash, name, role, email_verified, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'admin', true, NOW(), NOW())
		ON CONFLICT (email) DO NOTHING
	`, orgID, email, string(hashedPassword), name)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSeededAdminCreate, err)
	}

	slog.Info("seed admin created", "email", email)
	return seedAdminMembership(db, email)
}

// seedAdminMembership makes the seeded admin a member of its org: login resolves
// the org through memberships, and only migrated installs got one backfilled.
// It targets the account's home org, so an existing account is never granted
// admin in the SEED_ADMIN_ORG_SLUG org when the two differ.
func seedAdminMembership(db *sql.DB, email string) error {
	_, err := db.Exec(`
		INSERT INTO memberships (user_id, organization_id, role, created_at)
		SELECT id, organization_id, 'admin', NOW() FROM users WHERE email = $1
		ON CONFLICT (user_id, organization_id) DO NOTHING
	`, email)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSeedMembership, err)
	}
	return nil
}
