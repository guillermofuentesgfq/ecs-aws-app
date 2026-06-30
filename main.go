package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/aws/aws-xray-sdk-go/xray"
	_ "github.com/lib/pq"
)

// Config holds all parsed application configuration from environment variables.
type Config struct {
	Port            int
	DBHost          string
	DBPort          int
	DBName          string
	DBUser          string
	DBPassword      string
	DBSSLMode       string
	XRayDaemonAddr  string
	ShutdownTimeout time.Duration
}

// =============================================================================
// Configuration Parsing
// =============================================================================

// parseConfig reads and validates configuration from environment variables.
// Fails fast with descriptive errors when required values are missing or invalid.
func parseConfig() (*Config, error) {
	port, err := envInt("PORT", 8080)
	if err != nil {
		return nil, fmt.Errorf("PORT: %w", err)
	}

	dbPort, err := envInt("DB_PORT", 5432)
	if err != nil {
		return nil, fmt.Errorf("DB_PORT: %w", err)
	}

	cfg := &Config{
		Port:            port,
		DBHost:          os.Getenv("DB_HOST"),
		DBPort:          dbPort,
		DBName:          os.Getenv("DB_NAME"),
		DBUser:          os.Getenv("DB_USER"),
		DBPassword:      os.Getenv("DB_PASSWORD"),
		DBSSLMode:       envStr("DB_SSLMODE", "require"),
		XRayDaemonAddr:  envStr("AWS_XRAY_DAEMON_ADDRESS", "0.0.0.0:2000"),
		ShutdownTimeout: 30 * time.Second,
	}

	return cfg, nil
}

// envStr returns the environment variable value, or a default if not set.
func envStr(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

// envInt reads an integer environment variable, returning the default on empty
// or failing fast on invalid input.
func envInt(key string, fallback int) (int, error) {
	val := os.Getenv(key)
	if val == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("invalid integer value %q: %w", val, err)
	}
	return n, nil
}

// =============================================================================
// Database
// =============================================================================

// buildDSN constructs a PostgreSQL connection string from config.
func buildDSN(cfg *Config) string {
	return fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBUser, cfg.DBPassword, cfg.DBSSLMode,
	)
}

// openDB opens a connection pool to PostgreSQL.
func openDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Connection pool settings for RDS workloads.
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	return db, nil
}

// =============================================================================
// Handlers
// =============================================================================

// healthHandler returns 200 OK with no dependencies — suitable for L4
// load balancer health checks.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// readyHandler performs a SELECT 1 against the database. Returns 200 when the
// database is reachable, 503 when it is not.
func readyHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		err := db.QueryRowContext(ctx, "SELECT 1").Scan(new(int))
		if err != nil {
			slog.Warn("readiness check failed", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "unavailable"})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

// dbHandler executes a simple query and returns the latency. Creates an X-Ray
// subsegment for the database operation.
func dbHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		var latency time.Duration
		var queryErr error

		xray.Capture(ctx, "DBQuery", func(ctx context.Context) error {
			start := time.Now()
			err := db.QueryRowContext(ctx, "SELECT 1").Scan(new(int))
			latency = time.Since(start)
			queryErr = err
			return err
		})

		w.Header().Set("Content-Type", "application/json")

		if queryErr != nil {
			slog.Error("db query failed",
				"error", queryErr,
				"latency_ms", latency.Milliseconds(),
			)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": queryErr.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"db":         "ok",
			"latency_ms": latency.Milliseconds(),
		})
	}
}

// =============================================================================
// Middleware
// =============================================================================

// loggingMiddleware records structured request logs via slog.
type responseCapture struct {
	http.ResponseWriter
	status int
}

func (rc *responseCapture) WriteHeader(code int) {
	rc.status = code
	rc.ResponseWriter.WriteHeader(code)
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rc := &responseCapture{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rc, r)

		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rc.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// =============================================================================
// Server
// =============================================================================

func main() {
	// Configure structured JSON logging.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// Parse configuration — fail fast on invalid input.
	cfg, err := parseConfig()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	// Propagate X-Ray daemon address to the SDK.
	os.Setenv("AWS_XRAY_DAEMON_ADDRESS", cfg.XRayDaemonAddr)

	// Build DSN and open database connection.
	dsn := buildDSN(cfg)
	db, err := openDB(dsn)
	if err != nil {
		slog.Error("failed to open database connection", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Setup routes.
	mux := http.NewServeMux()

	// Health — no X-Ray wrapper for minimal overhead on L4 checks.
	// Logging is added inline since writing to stdout has no external dependency.
	mux.Handle("/health", loggingMiddleware(http.HandlerFunc(healthHandler)))

	// Readiness — checks DB health, traced with X-Ray.
	mux.Handle("/ready",
		xray.Handler(xray.NewFixedSegmentNamer("ecs-aws-app"),
			loggingMiddleware(readyHandler(db)),
		),
	)

	// DB query — executes query, reports latency, traced with X-Ray.
	mux.Handle("/api/db",
		xray.Handler(xray.NewFixedSegmentNamer("ecs-aws-app"),
			loggingMiddleware(dbHandler(db)),
		),
	)

	// Build the HTTP server.
	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine so we can listen for shutdown signals.
	go func() {
		slog.Info("server starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	// =========================================================================
	// Graceful Shutdown
	// =========================================================================

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	slog.Info("shutting down server", "signal", sig.String())

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("forced shutdown", "error", err)
		os.Exit(1)
	}

	slog.Info("server stopped gracefully")
}
