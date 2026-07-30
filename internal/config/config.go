
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


func Load() (*Config, error) {
	cfg := &Config{
		Env:              getEnv("ENV", "dev"),
		LogLevel:         getEnv("LOG_LEVEL", "info"),
		HTTPAddr:         getEnv("HTTP_ADDR", ":8080"),
		HTTPReadTimeout:  getEnvDuration("HTTP_READ_TIMEOUT", 5*time.Second),
		HTTPWriteTimeout: getEnvDuration("HTTP_WRITE_TIMEOUT", 10*time.Second),

		
		DatabaseURL: getEnv("DATABASE_URL",
			"postgres://content_pipeline_insider:content_pipeline_insider@localhost:5432/content_pipeline_insider?sslmode=disable"),
	}

	if cfg.Env == "prod" && cfg.DatabaseURL == "" {
		return nil, errors.New("DATABASE_URL is required in prod")
	}
	return cfg, nil
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		panic(fmt.Sprintf("config: bad duration for %s=%q: %v", key, v, err))
	}
	return d
}
