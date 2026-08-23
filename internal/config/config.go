package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address         string
	DatabasePath    string
	SessionTTL      time.Duration
	WorkerInterval  time.Duration
	ShutdownTimeout time.Duration
	LogLevel        string
	Bootstrap       Bootstrap
}

type Bootstrap struct {
	Enabled       bool
	DistrictID    string
	DistrictName  string
	AdminEmail    string
	AdminPassword string
}

func Load() (Config, error) {
	cfg := Config{
		Address:      env("SILVERCARE_ADDR", ":8080"),
		DatabasePath: env("SILVERCARE_DATABASE", "./data/silvercare.db"),
		LogLevel:     strings.ToLower(env("SILVERCARE_LOG_LEVEL", "info")),
		Bootstrap: Bootstrap{
			Enabled:       envBool("SILVERCARE_BOOTSTRAP", true),
			DistrictID:    env("SILVERCARE_BOOTSTRAP_DISTRICT_ID", "district_demo"),
			DistrictName:  env("SILVERCARE_BOOTSTRAP_DISTRICT_NAME", "示范街道"),
			AdminEmail:    env("SILVERCARE_BOOTSTRAP_EMAIL", "coordinator@example.test"),
			AdminPassword: env("SILVERCARE_BOOTSTRAP_PASSWORD", "change-me-now"),
		},
	}

	var err error
	if cfg.SessionTTL, err = envDuration("SILVERCARE_SESSION_TTL", 8*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.WorkerInterval, err = envDuration("SILVERCARE_WORKER_INTERVAL", 2*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = envDuration("SILVERCARE_SHUTDOWN_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Address) == "" {
		return fmt.Errorf("SILVERCARE_ADDR cannot be empty")
	}
	if strings.TrimSpace(c.DatabasePath) == "" {
		return fmt.Errorf("SILVERCARE_DATABASE cannot be empty")
	}
	if c.SessionTTL <= 0 {
		return fmt.Errorf("SILVERCARE_SESSION_TTL must be positive")
	}
	if c.WorkerInterval <= 0 {
		return fmt.Errorf("SILVERCARE_WORKER_INTERVAL must be positive")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("SILVERCARE_SHUTDOWN_TIMEOUT must be positive")
	}
	if c.Bootstrap.Enabled {
		if c.Bootstrap.DistrictID == "" || c.Bootstrap.AdminEmail == "" || len(c.Bootstrap.AdminPassword) < 10 {
			return fmt.Errorf("bootstrap requires district, email, and a password of at least 10 characters")
		}
	}
	return nil
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return parsed, nil
}

func envBool(key string, fallback bool) bool {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
