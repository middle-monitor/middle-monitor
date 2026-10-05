package workers

import (
	"database/sql"
	"log/slog"
	"time"

	"github.com/lib/pq"

	"middle-monitor/backend/services"
)

// retentionArgs are the per-organization cutoffs as SQL array parameters:
// unnest() pairs each organization with the timestamp its rows expire at.
type retentionArgs struct {
	orgIDs  pq.Int64Array
	cutoffs pq.GenericArray
}

// StartCleanupWorker starts a background worker that deletes data past each
// organization's plan retention (PostgreSQL + OpenSearch). Runs daily at 2 AM.
func StartCleanupWorker(db *sql.DB, opensearch *services.OpenSearchService) {
	go func() {
		// Run immediately on startup
		runCleanup(db, opensearch)

		// Then run daily at 2 AM
		for {
			now := time.Now()
			// Calculate next 2 AM
			nextRun := time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, now.Location())
			if now.After(nextRun) {
				// If it's already past 2 AM today, schedule for tomorrow
				nextRun = nextRun.Add(24 * time.Hour)
			}

			slog.Info("cleanup scheduled", "next_run", nextRun.Format(time.RFC3339))
			time.Sleep(nextRun.Sub(now))
			runCleanup(db, opensearch)
		}
	}()
	slog.Info("cleanup worker started", "free_days", services.FreeRetentionDays, "pro_days", services.ProRetentionDays)
}

func runCleanup(db *sql.DB, opensearch *services.OpenSearchService) {
	start := time.Now()

	// Idempotency keys and finished webhook deliveries are operational
	// bookkeeping, not customer data, so they are swept on a fixed window
	// rather than per-plan retention.
	cleanupOperationalTables(db)

	retentions, err := loadRetentions(db)
	if err != nil {
		slog.Error("cleanup retention lookup failed", "error", err)
		return
	}
	if len(retentions) == 0 {
		return
	}
	args := newRetentionArgs(retentions)

	// Cleanup OpenSearch: ALL indices (traces, logs, metrics, worker-results, errors)
	var totalOSCleaned int64
	if opensearch != nil {
		totalOSCleaned, err = opensearch.DeleteByRetention(retentions)
		if err != nil {
			slog.Error("cleanup opensearch failed", "error", err)
		}
	}

	cleanedResults, err := cleanupServiceResults(db, args)
	if err != nil {
		slog.Error("cleanup failed", "table", "service_results", "error", err)
	}

	cleanedErrors, err := cleanupApplicationErrors(db, args)
	if err != nil {
		slog.Error("cleanup failed", "table", "application_errors", "error", err)
	}

	if _, err := cleanupIngestUsage(db); err != nil {
		slog.Error("cleanup failed", "table", "ingest_usage", "error", err)
	}

	cleanedMetrics, err := cleanupSystemMetrics(db, args)
	if err != nil {
		slog.Error("cleanup failed", "table", "system_metrics", "error", err)
	}

	cleanedEvents, err := cleanupEvents(db, args)
	if err != nil {
		slog.Error("cleanup failed", "table", "events", "error", err)
	}

	cleanedProfiles, err := cleanupProfileCaptures(db, args)
	if err != nil {
		slog.Error("cleanup failed", "table", "profile_captures", "error", err)
	}

	totalPGCleaned := cleanedResults + cleanedErrors + cleanedMetrics + cleanedEvents + cleanedProfiles
	slog.Info("cleanup completed",
		"orgs", len(retentions),
		"postgres", totalPGCleaned,
		"opensearch", totalOSCleaned,
		"duration_ms", time.Since(start).Milliseconds())
}

// loadRetentions computes every organization's purge deadline from its effective
// plan (a running trial retains like Pro). Orgs on a plan that keeps data forever
// are left out so their rows are never matched.
func loadRetentions(db *sql.DB) ([]services.OrgRetention, error) {
	rows, err := db.Query(`
		SELECT id, COALESCE(plan, 'free'), trial_ends_at, COALESCE(custom_retention_days, 0)
		FROM organizations
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now().UTC()
	var retentions []services.OrgRetention
	for rows.Next() {
		var orgID int64
		var plan string
		var trialEndsAt sql.NullTime
		var customDays int
		if err := rows.Scan(&orgID, &plan, &trialEndsAt, &customDays); err != nil {
			return nil, err
		}
		var trial *time.Time
		if trialEndsAt.Valid {
			trial = &trialEndsAt.Time
		}
		days := services.RetentionDays(services.EffectivePlan(plan, trial), customDays)
		if days < 0 {
			continue
		}
		retentions = append(retentions, services.OrgRetention{
			OrgID:  orgID,
			Cutoff: now.AddDate(0, 0, -days),
		})
	}
	return retentions, rows.Err()
}

func newRetentionArgs(retentions []services.OrgRetention) retentionArgs {
	orgIDs := make(pq.Int64Array, len(retentions))
	cutoffs := make([]time.Time, len(retentions))
	for i, r := range retentions {
		orgIDs[i] = r.OrgID
		cutoffs[i] = r.Cutoff
	}
	return retentionArgs{orgIDs: orgIDs, cutoffs: pq.GenericArray{A: cutoffs}}
}

// service_results has no organization_id: it inherits its service's org.
func cleanupServiceResults(db *sql.DB, args retentionArgs) (int64, error) {
	result, err := db.Exec(`
		DELETE FROM service_results r
		USING services s, unnest($1::bigint[], $2::timestamptz[]) AS o(org_id, cutoff)
		WHERE r.service_id = s.id AND s.organization_id = o.org_id AND r.timestamp < o.cutoff
	`, args.orgIDs, args.cutoffs)
	if err != nil {
		return 0, err
	}
	rowsAffected, _ := result.RowsAffected()
	return rowsAffected, nil
}

// cleanupIngestUsage keeps a week of per-minute ingestion counters: Settings
// reads the last day, the rest is there to look back on an incident.
func cleanupIngestUsage(db *sql.DB) (int64, error) {
	result, err := db.Exec(`DELETE FROM ingest_usage WHERE minute < NOW() - INTERVAL '7 days'`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func cleanupApplicationErrors(db *sql.DB, args retentionArgs) (int64, error) {
	result, err := db.Exec(`
		DELETE FROM application_errors e
		USING unnest($1::bigint[], $2::timestamptz[]) AS o(org_id, cutoff)
		WHERE e.organization_id = o.org_id AND e.timestamp < o.cutoff
	`, args.orgIDs, args.cutoffs)
	if err != nil {
		return 0, err
	}
	rowsAffected, _ := result.RowsAffected()
	return rowsAffected, nil
}

func cleanupSystemMetrics(db *sql.DB, args retentionArgs) (int64, error) {
	result, err := db.Exec(`
		DELETE FROM system_metrics m
		USING unnest($1::bigint[], $2::timestamptz[]) AS o(org_id, cutoff)
		WHERE m.organization_id = o.org_id AND m.timestamp < o.cutoff
	`, args.orgIDs, args.cutoffs)
	if err != nil {
		return 0, err
	}
	rowsAffected, _ := result.RowsAffected()
	return rowsAffected, nil
}

func cleanupEvents(db *sql.DB, args retentionArgs) (int64, error) {
	result, err := db.Exec(`
		DELETE FROM events e
		USING unnest($1::bigint[], $2::timestamptz[]) AS o(org_id, cutoff)
		WHERE e.organization_id = o.org_id AND e.timestamp < o.cutoff
	`, args.orgIDs, args.cutoffs)
	if err != nil {
		return 0, err
	}
	rowsAffected, _ := result.RowsAffected()
	return rowsAffected, nil
}

func cleanupProfileCaptures(db *sql.DB, args retentionArgs) (int64, error) {
	result, err := db.Exec(`
		DELETE FROM profile_captures p
		USING unnest($1::bigint[], $2::timestamptz[]) AS o(org_id, cutoff)
		WHERE p.organization_id = o.org_id AND p.created_at < o.cutoff
	`, args.orgIDs, args.cutoffs)
	if err != nil {
		return 0, err
	}
	rowsAffected, _ := result.RowsAffected()
	return rowsAffected, nil
}

// cleanupOperationalTables drops the rows that only exist to make a retry or a
// debugging session work. A day covers any sane retry window; a month of
// delivery history is enough to debug an integration.
func cleanupOperationalTables(db *sql.DB) {
	if result, err := db.Exec(
		`DELETE FROM idempotency_keys WHERE created_at < NOW() - INTERVAL '1 day'`); err != nil {
		slog.Error("cleanup failed", "table", "idempotency_keys", "error", err)
	} else if rows, _ := result.RowsAffected(); rows > 0 {
		slog.Info("cleaned", "table", "idempotency_keys", "rows", rows)
	}

	if result, err := db.Exec(
		`DELETE FROM webhook_deliveries WHERE created_at < NOW() - INTERVAL '30 days'`); err != nil {
		slog.Error("cleanup failed", "table", "webhook_deliveries", "error", err)
	} else if rows, _ := result.RowsAffected(); rows > 0 {
		slog.Info("cleaned", "table", "webhook_deliveries", "rows", rows)
	}
}
