package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

type Config struct {
	Env              string        // "dev" | "staging" | "prod"
	LogLevel         string        // debug | info | warn | error
	HTTPAddr         string        // e.g. ":8080"
	HTTPReadTimeout  time.Duration // guards against slow-client attacks
	HTTPWriteTimeout time.Duration // caps how long a response can take

	DatabaseURL string
}

// EnvProd is the environment name that turns on production-only checks.
const EnvProd = "prod"

// devDatabaseURL matches docker-compose.yml so a fresh checkout runs with
// no environment set. It is applied outside production only.
const devDatabaseURL = "postgres://content_pipeline_insider:content_pipeline_insider@localhost:5432/content_pipeline_insider?sslmode=disable"

func Load() (*Config, error) {
	readTimeout, err := getEnvDuration("HTTP_READ_TIMEOUT", 5*time.Second)
	if err != nil {
		return nil, err
	}
	writeTimeout, err := getEnvDuration("HTTP_WRITE_TIMEOUT", 10*time.Second)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Env:              getEnv("ENV", "dev"),
		LogLevel:         getEnv("LOG_LEVEL", "info"),
		HTTPAddr:         getEnv("HTTP_ADDR", ":8080"),
		HTTPReadTimeout:  readTimeout,
		HTTPWriteTimeout: writeTimeout,

		// Read raw, with no default. The fallback is applied below and
		// only outside production, so the check that follows can tell
		// "nobody configured a database" from "somebody chose one".
		DatabaseURL: os.Getenv("DATABASE_URL"),
	}

	if cfg.DatabaseURL == "" {
		// Previously the development default was applied here
		// unconditionally, which made the production check below
		// unreachable: DatabaseURL was never empty, so a production
		// deployment missing DATABASE_URL started successfully and
		// connected to localhost instead of refusing to boot.
		if cfg.Env == EnvProd {
			return nil, errors.New("config: DATABASE_URL is required when ENV=prod")
		}
		cfg.DatabaseURL = devDatabaseURL
	}

	return cfg, nil
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// getEnvDuration returns an error rather than panicking on a malformed
// value. Every other failure in Load is reported to the caller, which
// logs it and exits non-zero; a panic here produced a stack trace for
// what is simply a typo in a deployment variable.
func getEnvDuration(key string, def time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config: bad duration for %s=%q: %w", key, v, err)
	}
	return d, nil
}
