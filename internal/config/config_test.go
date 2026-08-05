package config

import (
	"strings"
	"testing"
	"time"
)

// clearEnv unsets everything Load reads, so a test starts from a known
// state regardless of what the developer has exported in their shell.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"ENV", "LOG_LEVEL", "HTTP_ADDR",
		"HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "DATABASE_URL",
	} {
		t.Setenv(key, "")
	}
}

// The check this test covers was unreachable before: the development
// default was applied unconditionally, so DatabaseURL was never empty and
// a production deployment with no DATABASE_URL booted happily against
// localhost. Losing this test means losing the only thing standing
// between a missing environment variable and production reads going to
// an empty developer database.
func TestLoadRequiresDatabaseURLInProd(t *testing.T) {
	clearEnv(t)
	t.Setenv("ENV", EnvProd)

	cfg, err := Load()
	if err == nil {
		t.Fatalf("Load() = %+v, want an error when ENV=prod and DATABASE_URL is unset", cfg)
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("Load() error = %q, want it to name DATABASE_URL", err)
	}
}

func TestLoadDoesNotApplyTheDevDefaultInProd(t *testing.T) {
	clearEnv(t)
	t.Setenv("ENV", EnvProd)
	t.Setenv("DATABASE_URL", "postgres://user:pass@db.prod.internal:5432/app")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.DatabaseURL == devDatabaseURL {
		t.Fatal("Load() used the development database URL in production")
	}
	if !strings.Contains(cfg.DatabaseURL, "db.prod.internal") {
		t.Errorf("DatabaseURL = %q, want the configured value", cfg.DatabaseURL)
	}
}

func TestLoadDevelopmentDefaults(t *testing.T) {
	t.Run("falls back to the local database outside prod", func(t *testing.T) {
		clearEnv(t)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() = %v", err)
		}
		if cfg.DatabaseURL != devDatabaseURL {
			t.Errorf("DatabaseURL = %q, want the development default", cfg.DatabaseURL)
		}
		if cfg.Env != "dev" {
			t.Errorf("Env = %q, want dev", cfg.Env)
		}
		if cfg.HTTPAddr != ":8080" {
			t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
		}
		if cfg.HTTPReadTimeout != 5*time.Second {
			t.Errorf("HTTPReadTimeout = %v, want 5s", cfg.HTTPReadTimeout)
		}
	})

	t.Run("an explicit database URL still wins in dev", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("DATABASE_URL", "postgres://someone@elsewhere:5432/other")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() = %v", err)
		}
		if cfg.DatabaseURL != "postgres://someone@elsewhere:5432/other" {
			t.Errorf("DatabaseURL = %q, want the configured value", cfg.DatabaseURL)
		}
	})

	t.Run("staging is not production", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("ENV", "staging")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() = %v, want staging to fall back like dev", err)
		}
		if cfg.DatabaseURL != devDatabaseURL {
			t.Errorf("DatabaseURL = %q, want the development default", cfg.DatabaseURL)
		}
	})
}

// A malformed duration used to panic, producing a stack trace for what
// is a typo in a deployment variable. Every other failure in Load is
// returned to the caller, which logs it and exits non-zero.
func TestLoadRejectsMalformedDuration(t *testing.T) {
	for _, key := range []string{"HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT"} {
		t.Run(key, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(key, "30 seconds")

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Load() panicked with %v, want an error", r)
				}
			}()

			cfg, err := Load()
			if err == nil {
				t.Fatalf("Load() = %+v, want an error for a malformed duration", cfg)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("Load() error = %q, want it to name %s", err, key)
			}
		})
	}
}

func TestLoadReadsOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("HTTP_READ_TIMEOUT", "30s")
	t.Setenv("HTTP_WRITE_TIMEOUT", "1m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q, want :9090", cfg.HTTPAddr)
	}
	if cfg.HTTPReadTimeout != 30*time.Second {
		t.Errorf("HTTPReadTimeout = %v, want 30s", cfg.HTTPReadTimeout)
	}
	if cfg.HTTPWriteTimeout != time.Minute {
		t.Errorf("HTTPWriteTimeout = %v, want 1m", cfg.HTTPWriteTimeout)
	}
}
