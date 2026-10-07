// Package config loads runtime configuration from the environment.
//
// It deliberately contains only concerns that every module shares: the
// environment name, logging, HTTP serving, and the database. Anything specific
// to a capability (crawl budgets, provider keys) lives in the module that owns
// that capability, so platform never grows a dependency on the domain.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Prefix is prepended to every environment variable the platform reads.
const Prefix = "UPVISTA_"

// Config is the full runtime configuration.
type Config struct {
	Env      string
	Log      LogConfig
	HTTP     HTTPConfig
	Database DatabaseConfig
}

// LogConfig controls log output.
type LogConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

// HTTPConfig controls the shared server behaviour.
type HTTPConfig struct {
	Addr              string        `json:"addr"`
	ReadTimeout       time.Duration `json:"read_timeout"`
	ReadHeaderTimeout time.Duration `json:"read_header_timeout"`
	WriteTimeout      time.Duration `json:"write_timeout"`
	IdleTimeout       time.Duration `json:"idle_timeout"`
	ShutdownGrace     time.Duration `json:"shutdown_grace"`
	MaxBodyBytes      int64         `json:"max_body_bytes"`
}

// DatabaseConfig controls connection pooling.
type DatabaseConfig struct {
	URL              string        `json:"-"`
	MaxConns         int32         `json:"max_conns"`
	MinConns         int32         `json:"min_conns"`
	ConnectTimeout   time.Duration `json:"connect_timeout"`
	StatementTimeout time.Duration `json:"statement_timeout"`
	HealthCheck      time.Duration `json:"health_check_period"`
}

// Default returns a configuration that works locally with no environment set
// except DATABASE_URL.
func Default() Config {
	return Config{
		Env: "development",
		Log: LogConfig{Level: "info", Format: "json"},
		HTTP: HTTPConfig{
			Addr:              ":8080",
			ReadTimeout:       15 * time.Second,
			ReadHeaderTimeout: 5 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			ShutdownGrace:     20 * time.Second,
			MaxBodyBytes:      2 << 20,
		},
		Database: DatabaseConfig{
			MaxConns:         16,
			MinConns:         2,
			ConnectTimeout:   10 * time.Second,
			StatementTimeout: 30 * time.Second,
			HealthCheck:      30 * time.Second,
		},
	}
}

// Load reads configuration from the process environment, applying defaults for
// anything unset, then validates it.
func Load() (Config, error) {
	cfg := Default()
	var errs []error

	// A handful of names are conventional across the whole ecosystem rather
	// than ours. Accepting them unprefixed is not leniency, it is the
	// difference between an operator's HTTP_ADDR working and being silently
	// ignored because we invented a prefix they did not know about. The
	// prefixed form wins if both are set, so an override is never ambiguous.
	cfg.Env = envStringAny([]string{"ENV"}, cfg.Env)
	cfg.Log.Level = envStringAny([]string{"LOG_LEVEL"}, cfg.Log.Level)
	cfg.Log.Format = envStringAny([]string{"LOG_FORMAT"}, cfg.Log.Format)
	cfg.HTTP.Addr = envStringAny([]string{"HTTP_ADDR"}, cfg.HTTP.Addr)
	cfg.HTTP.ReadTimeout = envDuration("HTTP_READ_TIMEOUT", cfg.HTTP.ReadTimeout)
	cfg.HTTP.ReadHeaderTimeout = envDuration("HTTP_READ_HEADER_TIMEOUT", cfg.HTTP.ReadHeaderTimeout)
	cfg.HTTP.WriteTimeout = envDuration("HTTP_WRITE_TIMEOUT", cfg.HTTP.WriteTimeout)
	cfg.HTTP.IdleTimeout = envDuration("HTTP_IDLE_TIMEOUT", cfg.HTTP.IdleTimeout)
	cfg.HTTP.ShutdownGrace = envDuration("HTTP_SHUTDOWN_GRACE", cfg.HTTP.ShutdownGrace)
	cfg.HTTP.MaxBodyBytes = envInt64("HTTP_MAX_BODY_BYTES", cfg.HTTP.MaxBodyBytes)

	// DATABASE_URL is the most standard variable in the ecosystem and every
	// tool understands it, so it has always been read unprefixed.
	cfg.Database.URL = envStringAny([]string{"DATABASE_URL"}, os.Getenv("DATABASE_URL"))
	cfg.Database.MaxConns = int32(envInt64("DB_MAX_CONNS", int64(cfg.Database.MaxConns)))
	cfg.Database.MinConns = int32(envInt64("DB_MIN_CONNS", int64(cfg.Database.MinConns)))
	cfg.Database.ConnectTimeout = envDuration("DB_CONNECT_TIMEOUT", cfg.Database.ConnectTimeout)
	cfg.Database.StatementTimeout = envDuration("DB_STATEMENT_TIMEOUT", cfg.Database.StatementTimeout)
	cfg.Database.HealthCheck = envDuration("DB_HEALTH_CHECK_PERIOD", cfg.Database.HealthCheck)

	if err := cfg.Validate(); err != nil {
		errs = append(errs, err)
	}
	return cfg, errors.Join(errs...)
}

// Validate reports every problem with the configuration at once, so an operator
// fixes one boot instead of five.
func (c Config) Validate() error {
	var errs []error

	switch c.Env {
	case "development", "test", "staging", "production":
	default:
		errs = append(errs, fmt.Errorf("config: Env %q is not one of development, test, staging, production", c.Env))
	}

	if _, err := parseLevel(c.Log.Level); err != nil {
		errs = append(errs, err)
	}
	switch strings.ToLower(strings.TrimSpace(c.Log.Format)) {
	case "json", "text", "console":
	default:
		errs = append(errs, fmt.Errorf("config: Log.Format %q is not one of json, text, console", c.Log.Format))
	}

	if err := validateAddr(c.HTTP.Addr); err != nil {
		errs = append(errs, err)
	}
	if c.HTTP.ReadTimeout <= 0 || c.HTTP.WriteTimeout <= 0 {
		errs = append(errs, errors.New("config: HTTP read and write timeouts must be positive"))
	}
	if c.HTTP.ReadHeaderTimeout <= 0 {
		errs = append(errs, errors.New("config: HTTP ReadHeaderTimeout must be positive (slowloris protection)"))
	}
	if c.HTTP.ShutdownGrace <= 0 {
		errs = append(errs, errors.New("config: HTTP ShutdownGrace must be positive"))
	}
	if c.HTTP.MaxBodyBytes <= 0 {
		errs = append(errs, errors.New("config: HTTP MaxBodyBytes must be positive"))
	}

	if strings.TrimSpace(c.Database.URL) == "" {
		errs = append(errs, errors.New("config: DATABASE_URL is required"))
	}
	if c.Database.MaxConns < 1 {
		errs = append(errs, errors.New("config: DB MaxConns must be >= 1"))
	}
	if c.Database.MinConns < 0 || c.Database.MinConns > c.Database.MaxConns {
		errs = append(errs, fmt.Errorf("config: DB MinConns (%d) must be between 0 and MaxConns (%d)", c.Database.MinConns, c.Database.MaxConns))
	}
	if c.Database.StatementTimeout <= 0 {
		errs = append(errs, errors.New("config: DB StatementTimeout must be positive"))
	}

	return errors.Join(errs...)
}

// Redacted returns a copy safe to log or print: the database URL is reduced to
// its host so credentials never reach a log sink.
func (c Config) Redacted() Config {
	if c.Database.URL != "" {
		c.Database.URL = redactURL(c.Database.URL)
	}
	return c
}

func redactURL(raw string) string {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return "***"
	}
	host := rest
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		host = rest[at+1:]
	}
	if q := strings.IndexAny(host, "?/"); q >= 0 {
		host = host[:q]
	}
	return scheme + "://***@" + host
}

func validateAddr(addr string) error {
	if strings.TrimSpace(addr) == "" {
		return errors.New("config: HTTP Addr must not be empty")
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("config: HTTP Addr %q is not host:port: %w", addr, err)
	}
	return nil
}

func parseLevel(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug", "info", "warn", "warning", "error":
		return name, nil
	default:
		return "", fmt.Errorf("config: Log.Level %q is not one of debug, info, warn, error", name)
	}
}

// envStringAny returns the first name that is set and non-empty, preferring
// the prefixed form. The bare names are the ecosystem conventions; the prefixed
// ones exist so a deployment can disambiguate.
func envStringAny(names []string, def string) string {
	for _, n := range names {
		if v, ok := os.LookupEnv(Prefix + n); ok && v != "" {
			return v
		}
	}
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return def
}

func envString(name, def string) string {
	if v, ok := os.LookupEnv(Prefix + name); ok {
		return v
	}
	return def
}

func envDuration(name string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(Prefix + name)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}

func envInt64(name string, def int64) int64 {
	v, ok := os.LookupEnv(Prefix + name)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}
