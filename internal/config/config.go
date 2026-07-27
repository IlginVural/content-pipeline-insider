package config 


import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Env      string // "dev" | "staging" | "prod"
	LogLevel string // "debug" | "info" | "warn" | "error"

	HTTPAddr         string
	HTTPReadTimeout  time.Duration
	HTTPWriteTimeout time.Duration
}

func Load() (*Config, error) {
    cfg := &Config{
        Env:              getEnv("APP_ENV", "dev"),
        LogLevel:         getEnv("LOG_LEVEL", "info"),
        HTTPAddr:         getEnv("HTTP_ADDR", ":8080"),
        HTTPReadTimeout:  5 * time.Second,
        HTTPWriteTimeout: 10 * time.Second,
    }
	// Optional overrides for timeouts, given in whole seconds.
	if v := os.Getenv("HTTP_READ_TIMEOUT_SEC"); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("HTTP_READ_TIMEOUT_SEC invalid: %w", err)
		}
		cfg.HTTPReadTimeout = time.Duration(secs) * time.Second
	}
	if v := os.Getenv("HTTP_WRITE_TIMEOUT_SEC"); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("HTTP_WRITE_TIMEOUT_SEC invalid: %w", err)
		}
		cfg.HTTPWriteTimeout = time.Duration(secs) * time.Second
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}