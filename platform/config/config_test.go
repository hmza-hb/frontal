package config

import (
	"strings"
	"testing"
	"time"
)

const prefix = "UPVISTA_"

func valid() Config {
	c := Default()
	c.Database.URL = "postgres://u:p@localhost:5432/leads"
	return c
}

func TestDefaultIsValid(t *testing.T) {
	c := valid()
	if err := c.Validate(); err != nil {
		t.Fatalf("Default() is invalid: %v", err)
	}
}

func TestValidateRejectsMissingDatabaseURL(t *testing.T) {
	c := valid()
	c.Database.URL = "  "
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("Validate = %v, want a DATABASE_URL error", err)
	}
}

func TestValidateRejectsBadEnv(t *testing.T) {
	c := valid()
	c.Env = "prod"
	if err := c.Validate(); err == nil {
		t.Fatal("Validate accepted an unknown environment name")
	}
}

func TestValidateRejectsBadAddr(t *testing.T) {
	c := valid()
	c.HTTP.Addr = "not-a-host-port"
	if err := c.Validate(); err == nil {
		t.Fatal("Validate accepted a malformed listen address")
	}
}

func TestValidateRejectsMinConnsAboveMax(t *testing.T) {
	c := valid()
	c.Database.MinConns = 100
	c.Database.MaxConns = 10
	if err := c.Validate(); err == nil {
		t.Fatal("Validate accepted MinConns > MaxConns")
	}
}

func TestValidateRejectsZeroTimeouts(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"read timeout":    func(c *Config) { c.HTTP.ReadTimeout = 0 },
		"write timeout":   func(c *Config) { c.HTTP.WriteTimeout = 0 },
		"header timeout":  func(c *Config) { c.HTTP.ReadHeaderTimeout = 0 },
		"grace":           func(c *Config) { c.HTTP.ShutdownGrace = 0 },
		"max body":        func(c *Config) { c.HTTP.MaxBodyBytes = 0 },
		"statement limit": func(c *Config) { c.Database.StatementTimeout = 0 },
	} {
		c := valid()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("Validate accepted a zero %s", name)
		}
	}
}

func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	c := valid()
	c.Env = "nope"
	c.Log.Level = "loud"
	c.HTTP.Addr = "bad"
	c.Database.URL = ""
	err := c.Validate()
	if err == nil {
		t.Fatal("Validate returned nil for a badly broken config")
	}
	for _, want := range []string{"Env", "Log.Level", "Addr", "DATABASE_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate omitted %q from: %v", want, err)
		}
	}
}

func TestLoadUsesDefaultsAndOverrides(t *testing.T) {
	t.Setenv(prefix+"ENV", "test")
	t.Setenv(Prefix+"LOG_LEVEL", "debug")
	t.Setenv("DATABASE_URL", "postgres://u:p@127.0.0.1:5432/leads")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env != "test" || cfg.Log.Level != "debug" {
		t.Errorf("overrides ignored: %+v", cfg.Log)
	}
	if cfg.HTTP.Addr != Default().HTTP.Addr {
		t.Errorf("unset HTTP Addr = %q, want the default", cfg.HTTP.Addr)
	}
	if cfg.HTTP.ReadTimeout != 15*time.Second {
		t.Errorf("ReadTimeout = %s, want the default 15s", cfg.HTTP.ReadTimeout)
	}
}

func TestLoadIgnoresUnparseableValues(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@127.0.0.1:5432/leads")
	t.Setenv(prefix+"HTTP_READ_TIMEOUT", "not-a-duration")
	t.Setenv(prefix+"DB_MAX_CONNS", "many")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.ReadTimeout != 15*time.Second {
		t.Errorf("bad duration did not fall back to the default: %s", cfg.HTTP.ReadTimeout)
	}
	if cfg.Database.MaxConns != 16 {
		t.Errorf("bad int did not fall back to the default: %d", cfg.Database.MaxConns)
	}
}

func TestRedactedHidesCredentials(t *testing.T) {
	c := valid()
	c.Database.URL = "postgres://admin:hunter2@db.internal:5432/leads?sslmode=require"
	got := c.Redacted().Database.URL
	if strings.Contains(got, "hunter2") || strings.Contains(got, "admin") {
		t.Fatalf("Redacted leaked credentials: %s", got)
	}
	if !strings.Contains(got, "db.internal:5432") {
		t.Errorf("Redacted dropped the host, which is the useful part: %s", got)
	}
	if c.Database.URL == got {
		t.Error("Redacted mutated the receiver instead of returning a copy")
	}
}

func TestRedactedHandlesGarbage(t *testing.T) {
	c := Config{}
	c.Database.URL = "not a url"
	if got := c.Redacted().Database.URL; got == "not a url" {
		t.Error("Redacted passed through a value with no scheme")
	}
}

func TestLoadAcceptsUnprefixedConventionalNames(t *testing.T) {
	// An operator who sets HTTP_ADDR must not silently get :8080 instead. The
	// prefixed form still wins so a deployment can be explicit.
	t.Setenv("HTTP_ADDR", "127.0.0.1:9999")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.Addr != "127.0.0.1:9999" {
		t.Errorf("Addr = %q, want the unprefixed HTTP_ADDR to be honoured", cfg.HTTP.Addr)
	}
	if cfg.Log.Level != "debug" {
		t.Errorf("Log.Level = %q, want debug", cfg.Log.Level)
	}
	if cfg.Log.Format != "text" {
		t.Errorf("Log.Format = %q, want text", cfg.Log.Format)
	}
	if cfg.Database.URL != "postgres://u:p@localhost:5432/db" {
		t.Errorf("Database.URL = %q, want the unprefixed DATABASE_URL", cfg.Database.URL)
	}
}

func TestPrefixedNameWinsOverUnprefixed(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("HTTP_ADDR", "127.0.0.1:1111")
	t.Setenv(Prefix+"HTTP_ADDR", "127.0.0.1:2222")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.Addr != "127.0.0.1:2222" {
		t.Errorf("Addr = %q, want the prefixed variable to win", cfg.HTTP.Addr)
	}
}

func TestEmptyUnprefixedNameFallsBackToDefault(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db")
	t.Setenv("HTTP_ADDR", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.Addr != Default().HTTP.Addr {
		t.Errorf("Addr = %q, want the default for an empty variable", cfg.HTTP.Addr)
	}
}
