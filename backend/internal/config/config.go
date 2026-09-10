// Package config loads runtime configuration from the environment.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration. Values come from environment
// variables with sane development defaults so `go run ./cmd/server` works
// with no setup.
type Config struct {
	Env                string     // development | production
	Port               int        // listen port
	DBPath             string     // SQLite file path
	BillsPath          string     // directory for uploaded receipt images
	CORSAllowedOrigins []string   // allowed CORS origins (empty = same-origin only)
	LogLevel           slog.Level // debug | info | warn | error
	AIEncryptionKey    string     // passphrase for encrypting AI API keys at rest
	LLMTimeout         time.Duration
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	ShutdownTimeout    time.Duration
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	port, err := envInt("PORT", 8080)
	if err != nil {
		return Config{}, fmt.Errorf("PORT: %w", err)
	}

	logLevel, err := envLogLevel("LOG_LEVEL", slog.LevelInfo)
	if err != nil {
		return Config{}, fmt.Errorf("LOG_LEVEL: %w", err)
	}

	// Vision models on a local Ollama can take minutes on large receipts, so
	// the ceiling is generous by default. The write deadline trails it so a
	// normal request is never cut short by the HTTP server itself.
	llmTimeout, err := envDuration("LLM_TIMEOUT", 5*time.Minute)
	if err != nil {
		return Config{}, fmt.Errorf("LLM_TIMEOUT: %w", err)
	}

	return Config{
		Env:                envString("APP_ENV", "development"),
		Port:               port,
		DBPath:             envString("DB_PATH", "./data/finance.db"),
		BillsPath:          envString("BILLS_PATH", "./data/bills"),
		CORSAllowedOrigins: envList("CORS_ALLOWED_ORIGINS", ""),
		LogLevel:           logLevel,
		AIEncryptionKey:    envString("AI_ENCRYPTION_KEY", ""),
		LLMTimeout:         llmTimeout,
		ReadTimeout:        30 * time.Second,
		WriteTimeout:       llmTimeout + 60*time.Second,
		ShutdownTimeout:    15 * time.Second,
	}, nil
}

// IsProduction reports whether the app runs in production mode.
func (c Config) IsProduction() bool { return c.Env == "production" }

func envString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid integer %q", v)
	}
	return n, nil
}

// envDuration parses a Go duration string ("90s", "5m", "2m30s").
func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid duration %q (want e.g. 90s or 5m)", v)
	}
	return d, nil
}

func envList(key, fallback string) []string {
	v := os.Getenv(key)
	if v == "" {
		v = fallback
	}
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envLogLevel(key string, fallback slog.Level) (slog.Level, error) {
	switch strings.ToLower(envString(key, "")) {
	case "":
		return fallback, nil
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return fallback, fmt.Errorf("unknown level, want debug|info|warn|error")
	}
}
