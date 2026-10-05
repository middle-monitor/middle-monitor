package workers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"middle-monitor/backend/models"
	"middle-monitor/backend/services"
)

var (
	sqlConnectionPools = make(map[string]*sql.DB)
	sqlPoolsMutex      sync.RWMutex
)

// normalizeSQLEngine maps the engine stored in the service credentials to one of
// the supported engine families. MariaDB is wire-compatible with MySQL, so it
// shares the MySQL driver and diagnostics. An empty/unknown engine defaults to
// PostgreSQL to preserve backward compatibility with services created before
// multi-engine support existed.
func normalizeSQLEngine(engine string) string {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "mysql", "mariadb":
		return "mysql"
	default:
		return "postgres"
	}
}

// getSQLConnectionPool returns a shared database connection pool for the given DSN
// This allows connections to be reused, significantly reducing latency
func getSQLConnectionPool(driver, dsn string) (*sql.DB, error) {
	sqlPoolsMutex.RLock()
	if pool, exists := sqlConnectionPools[dsn]; exists {
		sqlPoolsMutex.RUnlock()
		// Verify the connection is still alive
		if err := pool.Ping(); err == nil {
			return pool, nil
		}
		// Connection is dead, remove it and create a new one
		sqlPoolsMutex.Lock()
		delete(sqlConnectionPools, dsn)
		sqlPoolsMutex.Unlock()
	} else {
		sqlPoolsMutex.RUnlock()
	}

	// Create new connection pool
	sqlPoolsMutex.Lock()
	defer sqlPoolsMutex.Unlock()

	// Double-check after acquiring write lock
	if pool, exists := sqlConnectionPools[dsn]; exists {
		return pool, nil
	}

	// Configure connection pool for optimal performance
	pool, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}

	// Set connection pool parameters
	pool.SetMaxOpenConns(10)                  // Maximum number of open connections
	pool.SetMaxIdleConns(5)                   // Maximum number of idle connections
	pool.SetConnMaxLifetime(30 * time.Minute) // Maximum connection lifetime
	pool.SetConnMaxIdleTime(10 * time.Minute) // Maximum idle time

	// Test the connection
	if err := pool.Ping(); err != nil {
		pool.Close()
		return nil, err
	}

	sqlConnectionPools[dsn] = pool
	return pool, nil
}

func executeSQLService(db *sql.DB, service models.Service) models.ServiceResult {
	result := models.ServiceResult{
		ServiceID: service.ID,
		Timestamp: time.Now().UTC(),
	}

	// Parse SQL credentials from service
	var sqlEngine, sqlHost, sqlPort, sqlDatabase, sqlUser, sqlPassword string
	sqlEngine = "postgres" // Default engine for backward compatibility
	sqlHost = service.Host // Default to service host

	if service.Credentials != nil && *service.Credentials != "" {
		var creds map[string]interface{}
		if err := json.Unmarshal([]byte(*service.Credentials), &creds); err == nil {
			if engine, ok := creds["engine"].(string); ok && engine != "" {
				sqlEngine = engine
			}
			if host, ok := creds["host"].(string); ok && host != "" {
				sqlHost = host
			}
			if port, ok := creds["port"].(string); ok && port != "" {
				sqlPort = port
			}
			if database, ok := creds["database"].(string); ok && database != "" {
				sqlDatabase = database
			}
			if user, ok := creds["username"].(string); ok && user != "" {
				sqlUser = user
			}
		}
		// Decrypt password — handles both encrypted (password_enc) and legacy plain-text (password).
		if plain, err := services.DecryptSQLPassword(*service.Credentials); err == nil && plain != "" {
			sqlPassword = plain
		}
	}

	engine := normalizeSQLEngine(sqlEngine)

	// Apply engine-specific defaults for any field the user left empty.
	if sqlPort == "" {
		if engine == "mysql" {
			sqlPort = "3306"
		} else {
			sqlPort = "5432"
		}
	}
	if sqlDatabase == "" {
		if engine == "mysql" {
			sqlDatabase = "mysql"
		} else {
			sqlDatabase = "postgres"
		}
	}

	// Build DSN and pick the driver for the target engine.
	var driver, dsn string
	switch engine {
	case "mysql":
		driver = "mysql"
		auth := ""
		if sqlUser != "" {
			auth = sqlUser
			if sqlPassword != "" {
				auth += ":" + sqlPassword
			}
			auth += "@"
		}
		dsn = fmt.Sprintf("%stcp(%s:%s)/%s?timeout=5s", auth, sqlHost, sqlPort, sqlDatabase)
	default:
		driver = "postgres"
		// connect_timeout mirrors the mysql DSN timeout: without it lib/pq blocks on
		// the kernel SYN retries, well past the check interval.
		dsn = fmt.Sprintf("host=%s port=%s dbname=%s sslmode=disable connect_timeout=5", sqlHost, sqlPort, sqlDatabase)
		if sqlUser != "" {
			dsn += fmt.Sprintf(" user=%s", sqlUser)
		}
		if sqlPassword != "" {
			dsn += fmt.Sprintf(" password=%s", sqlPassword)
		}
	}

	// Use shared connection pool for connection reuse across all checks
	// This allows database connections to be reused, significantly reducing latency
	checkDB, err := getSQLConnectionPool(driver, dsn)
	if err != nil {
		result.Status = "failure"
		msg := fmt.Sprintf("Failed to get database connection pool: %v", err)
		result.Message = &msg
		slog.Error("sql check: failed to get connection pool", "error", err)
		return result
	}

	// Execute SQL ping check
	// With connection pooling, this should be very fast if a connection is already available
	start := time.Now()
	err = checkDB.Ping()
	latency := float64(time.Since(start).Milliseconds())

	if err != nil {
		result.Status = "failure"
		msg := fmt.Sprintf("Database connection failed: %v", err)
		result.Message = &msg
		result.Latency = &latency
		slog.Error("sql check: ping failed", "latency_ms", latency, "error", err)
		// Remove dead connection from pool
		sqlPoolsMutex.Lock()
		delete(sqlConnectionPools, dsn)
		sqlPoolsMutex.Unlock()
		return result
	}

	// Log latency only if it's significant (with connection reuse, it should be very fast)
	if latency > 10 {
		slog.Debug("sql check: connection successful", "latency_ms", latency)
	}

	// Connection successful, now perform engine-specific advanced checks
	var checks map[string]interface{}
	var hasWarnings bool
	switch engine {
	case "mysql":
		checks, hasWarnings = runMySQLDiagnostics(checkDB, sqlDatabase, latency)
	default:
		checks, hasWarnings = runPostgresDiagnostics(checkDB, sqlDatabase, latency)
	}

	// Build result message with all checks
	checksJSON, _ := json.Marshal(checks)
	checksStr := string(checksJSON)

	// critical_threshold overrides internal heuristics: if connection latency exceeds it, mark as failure
	if service.CriticalThreshold != nil && latency > *service.CriticalThreshold {
		result.Status = "failure"
		msg := fmt.Sprintf("DB connection latency (%.0fms) exceeded critical threshold (%.0fms). Details: %s", latency, *service.CriticalThreshold, checksStr)
		result.Message = &msg
	} else if hasWarnings {
		result.Status = "warning"
		msg := fmt.Sprintf("Database check completed with warnings (%.2fms). Details: %s", latency, checksStr)
		result.Message = &msg
	} else {
		result.Status = "success"
		msg := fmt.Sprintf("Database check successful (%.2fms). Details: %s", latency, checksStr)
		result.Message = &msg
	}
	result.Latency = &latency

	return result
}

// runPostgresDiagnostics performs PostgreSQL-specific health checks using the
// pg_* system catalogs. It returns the collected metrics and whether any of them
// crossed a warning heuristic.
func runPostgresDiagnostics(checkDB *sql.DB, sqlDatabase string, latency float64) (map[string]interface{}, bool) {
	checks := make(map[string]interface{})
	checks["connection_latency_ms"] = latency
	hasWarnings := false

	// 1. Check active connections
	var activeConnections int
	err := checkDB.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = $1`, sqlDatabase).Scan(&activeConnections)
	if err == nil {
		checks["active_connections"] = activeConnections
		// Warning if too many connections (>80% of max_connections)
		var maxConnections int
		err2 := checkDB.QueryRow(`SELECT setting::int FROM pg_settings WHERE name = 'max_connections'`).Scan(&maxConnections)
		if err2 == nil && maxConnections > 0 {
			connectionPercent := float64(activeConnections) / float64(maxConnections) * 100
			checks["connection_usage_percent"] = connectionPercent
			if connectionPercent > 80 {
				hasWarnings = true
				checks["connection_warning"] = fmt.Sprintf("High connection usage: %.1f%% (%d/%d)", connectionPercent, activeConnections, maxConnections)
			}
		}
	}

	// 2. Check for slow queries (queries running > 5 seconds)
	var slowQueries int
	var slowQueriesDetails []map[string]interface{}
	err = checkDB.QueryRow(`
		SELECT count(*)
		FROM pg_stat_activity
		WHERE datname = $1
		AND state = 'active'
		AND now() - query_start > interval '5 seconds'
	`, sqlDatabase).Scan(&slowQueries)
	if err == nil && slowQueries > 0 {
		checks["slow_queries_count"] = slowQueries
		hasWarnings = true
		// Get details of slow queries
		rows, err2 := checkDB.Query(`
			SELECT pid, usename, application_name, client_addr, state,
				   now() - query_start as duration,
				   substring(query, 1, 100) as query_preview
			FROM pg_stat_activity
			WHERE datname = $1
			AND state = 'active'
			AND now() - query_start > interval '5 seconds'
			ORDER BY query_start
			LIMIT 10
		`, sqlDatabase)
		if err2 == nil {
			defer rows.Close()
			for rows.Next() {
				var pid int
				var user, app, client, state, queryPreview string
				var duration string
				if err3 := rows.Scan(&pid, &user, &app, &client, &state, &duration, &queryPreview); err3 == nil {
					slowQueriesDetails = append(slowQueriesDetails, map[string]interface{}{
						"pid":           pid,
						"user":          user,
						"application":   app,
						"client":        client,
						"duration":      duration,
						"query_preview": queryPreview,
					})
				}
			}
			checks["slow_queries"] = slowQueriesDetails
		}
	}

	// 3. Check for locks/blocks
	var blockedQueries int
	err = checkDB.QueryRow(`
		SELECT count(DISTINCT blocked_locks.pid)
		FROM pg_catalog.pg_locks blocked_locks
		JOIN pg_catalog.pg_stat_activity blocking_activity ON blocking_activity.pid = blocked_locks.pid
		JOIN pg_catalog.pg_locks blocking_locks ON blocking_locks.locktype = blocked_locks.locktype
			AND blocking_locks.database IS NOT DISTINCT FROM blocked_locks.database
			AND blocking_locks.relation IS NOT DISTINCT FROM blocked_locks.relation
			AND blocking_locks.page IS NOT DISTINCT FROM blocked_locks.page
			AND blocking_locks.tuple IS NOT DISTINCT FROM blocked_locks.tuple
			AND blocking_locks.virtualxid IS NOT DISTINCT FROM blocked_locks.virtualxid
			AND blocking_locks.transactionid IS NOT DISTINCT FROM blocked_locks.transactionid
			AND blocking_locks.classid IS NOT DISTINCT FROM blocked_locks.classid
			AND blocking_locks.objid IS NOT DISTINCT FROM blocked_locks.objid
			AND blocking_locks.objsubid IS NOT DISTINCT FROM blocked_locks.objsubid
			AND blocking_locks.pid != blocked_locks.pid
		JOIN pg_catalog.pg_stat_activity blocked_activity ON blocked_activity.pid = blocked_locks.pid
		WHERE NOT blocked_locks.granted
	`).Scan(&blockedQueries)
	if err == nil && blockedQueries > 0 {
		checks["blocked_queries"] = blockedQueries
		hasWarnings = true
		checks["blocked_warning"] = fmt.Sprintf("%d queries are blocked by locks", blockedQueries)
	}

	// 4. Check database size
	var dbSize int64
	err = checkDB.QueryRow(`SELECT pg_database_size($1)`, sqlDatabase).Scan(&dbSize)
	if err == nil {
		dbSizeGB := float64(dbSize) / (1024 * 1024 * 1024)
		checks["database_size_gb"] = dbSizeGB
	}

	// 5. Check for dead tuples (bloat indicator) - check per table, not aggregate
	// Get tables with significant dead tuples (>20% or >1000 dead tuples)
	var tablesWithBloat []map[string]interface{}
	bloatRows, err := checkDB.Query(`
		SELECT
			relname as tablename,
			n_dead_tup as dead_tuples,
			n_live_tup as live_tuples,
			CASE
				WHEN n_live_tup + n_dead_tup > 0
				THEN ROUND((n_dead_tup::numeric / (n_live_tup + n_dead_tup) * 100)::numeric, 2)
				ELSE 0
			END as dead_percent
		FROM pg_stat_user_tables
		WHERE n_dead_tup > 0
		  AND (
			(n_live_tup + n_dead_tup > 0 AND (n_dead_tup::numeric / (n_live_tup + n_dead_tup) > 0.2))
			OR n_dead_tup > 1000
		  )
		ORDER BY dead_percent DESC, n_dead_tup DESC
		LIMIT 10
	`)
	if err == nil {
		defer bloatRows.Close()
		for bloatRows.Next() {
			var tableName string
			var deadTuples, liveTuples int64
			var deadPercent float64
			if err2 := bloatRows.Scan(&tableName, &deadTuples, &liveTuples, &deadPercent); err2 == nil {
				tablesWithBloat = append(tablesWithBloat, map[string]interface{}{
					"table":        tableName,
					"dead_tuples":  deadTuples,
					"live_tuples":  liveTuples,
					"dead_percent": deadPercent,
				})
			}
		}
		if len(tablesWithBloat) > 0 {
			checks["tables_with_bloat"] = tablesWithBloat
			hasWarnings = true
			// Get the worst table for the summary
			worstTable := tablesWithBloat[0]
			worstTableName := worstTable["table"].(string)
			worstDeadPercent := worstTable["dead_percent"].(float64)
			worstDeadTuples := worstTable["dead_tuples"].(int64)
			worstLiveTuples := worstTable["live_tuples"].(int64)
			checks["dead_tuples_warning"] = fmt.Sprintf("High dead tuples in %s: %.1f%% (%d dead / %d total)",
				worstTableName, worstDeadPercent, worstDeadTuples, worstDeadTuples+worstLiveTuples)
		}
		// Also calculate global stats for reference
		var totalDeadTuples, totalLiveTuples int64
		err3 := checkDB.QueryRow(`
			SELECT
				sum(n_dead_tup) as dead_tuples,
				sum(n_live_tup) as live_tuples
			FROM pg_stat_user_tables
		`).Scan(&totalDeadTuples, &totalLiveTuples)
		if err3 == nil && totalLiveTuples+totalDeadTuples > 0 {
			globalDeadPercent := float64(totalDeadTuples) / float64(totalLiveTuples+totalDeadTuples) * 100
			checks["global_dead_tuples"] = totalDeadTuples
			checks["global_dead_tuples_percent"] = globalDeadPercent
		}
	}

	// 6. Check for long-running transactions
	var longTransactions int
	err = checkDB.QueryRow(`
		SELECT count(*)
		FROM pg_stat_activity
		WHERE datname = $1
		AND state = 'idle in transaction'
		AND now() - state_change > interval '1 minute'
	`, sqlDatabase).Scan(&longTransactions)
	if err == nil && longTransactions > 0 {
		checks["long_transactions"] = longTransactions
		hasWarnings = true
		checks["long_transactions_warning"] = fmt.Sprintf("%d long-running idle transactions", longTransactions)
	}

	// 7. Check replication lag (if replica)
	var replicationLag *float64
	err = checkDB.QueryRow(`
		SELECT EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp())) * 1000
		WHERE pg_is_in_recovery()
	`).Scan(&replicationLag)
	if err == nil && replicationLag != nil {
		checks["replication_lag_ms"] = *replicationLag
		if *replicationLag > 10000 { // > 10 seconds
			hasWarnings = true
			checks["replication_lag_warning"] = fmt.Sprintf("High replication lag: %.0fms", *replicationLag)
		}
	}

	// 8. Check for errors in pg_stat_database (if available)
	var numDeadlocks int64
	err = checkDB.QueryRow(`
		SELECT deadlocks
		FROM pg_stat_database
		WHERE datname = $1
	`, sqlDatabase).Scan(&numDeadlocks)
	if err == nil {
		checks["deadlocks"] = numDeadlocks
	}

	return checks, hasWarnings
}

// runMySQLDiagnostics performs MySQL/MariaDB-specific health checks using the
// information_schema views. Each query degrades gracefully: a missing privilege
// or unsupported view simply omits that metric instead of failing the check.
func runMySQLDiagnostics(checkDB *sql.DB, sqlDatabase string, latency float64) (map[string]interface{}, bool) {
	checks := make(map[string]interface{})
	checks["connection_latency_ms"] = latency
	hasWarnings := false

	// 1. Active connections vs max_connections
	var activeConnections int
	if err := checkDB.QueryRow(`SELECT COUNT(*) FROM information_schema.PROCESSLIST`).Scan(&activeConnections); err == nil {
		checks["active_connections"] = activeConnections
		var maxConnections int
		if err2 := checkDB.QueryRow(`SELECT @@max_connections`).Scan(&maxConnections); err2 == nil && maxConnections > 0 {
			connectionPercent := float64(activeConnections) / float64(maxConnections) * 100
			checks["connection_usage_percent"] = connectionPercent
			if connectionPercent > 80 {
				hasWarnings = true
				checks["connection_warning"] = fmt.Sprintf("High connection usage: %.1f%% (%d/%d)", connectionPercent, activeConnections, maxConnections)
			}
		}
	}

	// 2. Slow queries (running > 5 seconds, excluding idle "Sleep" connections)
	var slowQueries int
	if err := checkDB.QueryRow(`SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE COMMAND <> 'Sleep' AND TIME > 5`).Scan(&slowQueries); err == nil && slowQueries > 0 {
		checks["slow_queries_count"] = slowQueries
		hasWarnings = true
		rows, err2 := checkDB.Query(`
			SELECT ID, COALESCE(USER,''), COALESCE(HOST,''), COALESCE(DB,''), COALESCE(COMMAND,''), TIME, COALESCE(LEFT(INFO,100),'')
			FROM information_schema.PROCESSLIST
			WHERE COMMAND <> 'Sleep' AND TIME > 5
			ORDER BY TIME DESC
			LIMIT 10
		`)
		if err2 == nil {
			defer rows.Close()
			var details []map[string]interface{}
			for rows.Next() {
				var id, duration int64
				var user, host, db, command, queryPreview string
				if err3 := rows.Scan(&id, &user, &host, &db, &command, &duration, &queryPreview); err3 == nil {
					details = append(details, map[string]interface{}{
						"pid":           id,
						"user":          user,
						"client":        host,
						"database":      db,
						"command":       command,
						"duration":      fmt.Sprintf("%ds", duration),
						"query_preview": queryPreview,
					})
				}
			}
			checks["slow_queries"] = details
		}
	}

	// 3. Database size (data + indexes for the target schema)
	var dbSize int64
	if err := checkDB.QueryRow(`
		SELECT COALESCE(SUM(data_length + index_length), 0)
		FROM information_schema.TABLES
		WHERE table_schema = ?
	`, sqlDatabase).Scan(&dbSize); err == nil {
		checks["database_size_gb"] = float64(dbSize) / (1024 * 1024 * 1024)
	}

	// 4. Long-running transactions (open > 1 minute)
	var longTransactions int
	if err := checkDB.QueryRow(`
		SELECT COUNT(*)
		FROM information_schema.INNODB_TRX
		WHERE TIMESTAMPDIFF(SECOND, trx_started, NOW()) > 60
	`).Scan(&longTransactions); err == nil && longTransactions > 0 {
		checks["long_transactions"] = longTransactions
		hasWarnings = true
		checks["long_transactions_warning"] = fmt.Sprintf("%d long-running transactions", longTransactions)
	}

	return checks, hasWarnings
}
