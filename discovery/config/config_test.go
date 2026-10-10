package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolate clears every environment variable this package reads so a test never
// inherits a value from the developer's shell or a previous test.
func isolate(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "ENV" || name == "LOG_LEVEL" || name == "LOG_FORMAT" ||
			name == "DATABASE_URL" || name == "HTTP_ADDR" ||
			strings.HasPrefix(name, Prefix) {
			t.Setenv(name, "")
			os.Unsetenv(name)
		}
	}
}

func TestDefaultIsValid(t *testing.T) {
	isolate(t)
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the default configuration does not validate: %v", err)
	}
}

func TestLoadDefaultsWithoutEnvironment(t *testing.T) {
	isolate(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.Env != "development" {
		t.Errorf("Env = %q, want development", cfg.Env)
	}
	if cfg.Run.MaxProviderCalls <= 0 {
		t.Error("MaxProviderCalls must have a usable default")
	}
}

func TestLoadReadsPrefixedEnvironment(t *testing.T) {
	isolate(t)
	t.Setenv(Prefix+"RUN_MAX_PROVIDER_CALLS", "1234")
	t.Setenv(Prefix+"RANK_ACCEPT_THRESHOLD", "0.8")
	t.Setenv(Prefix+"PROVIDER_MAX_CALLS", "100")
	t.Setenv(Prefix+"QUERY_INCLUDE_LOCAL_LANGUAGE", "false")
	t.Setenv(Prefix+"PROVIDERS_DISABLED", "search, certificate")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.Run.MaxProviderCalls != 1234 {
		t.Errorf("MaxProviderCalls = %d, want 1234", cfg.Run.MaxProviderCalls)
	}
	if cfg.Rank.AcceptThreshold != 0.8 {
		t.Errorf("AcceptThreshold = %v, want 0.8", cfg.Rank.AcceptThreshold)
	}
	if cfg.Prov.PerProviderCalls != 100 {
		t.Errorf("PerProviderCalls = %d, want 100", cfg.Prov.PerProviderCalls)
	}
	if cfg.Query.IncludeLocalLanguage {
		t.Error("IncludeLocalLanguage = true, want false")
	}
	if len(cfg.Prov.Disabled) != 2 {
		t.Errorf("Disabled = %v, want two entries", cfg.Prov.Disabled)
	}
}

func TestConventionalNamesAreAcceptedUnprefixed(t *testing.T) {
	// An operator's HTTP_ADDR has to work. Inventing a prefix they do not know
	// about makes the setting silently ignored.
	isolate(t)
	t.Setenv("HTTP_ADDR", ":9999")
	t.Setenv("ENV", "test")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.API.Addr != ":9999" {
		t.Errorf("API.Addr = %q, want :9999 from the unprefixed name", cfg.API.Addr)
	}
	if cfg.Env != "test" {
		t.Errorf("Env = %q, want test", cfg.Env)
	}
}

func TestPrefixedNameBeatsUnprefixed(t *testing.T) {
	isolate(t)
	t.Setenv("HTTP_ADDR", ":9999")
	t.Setenv(Prefix+"HTTP_ADDR", ":7777")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.API.Addr != ":7777" {
		t.Errorf("API.Addr = %q, want the prefixed value to win", cfg.API.Addr)
	}
}

func TestMalformedNumbersFailLoudly(t *testing.T) {
	// A typo'd integer must not silently fall back to the default. The operator
	// would see a run that "worked" and had a budget they never set.
	isolate(t)
	t.Setenv(Prefix+"RUN_MAX_PROVIDER_CALLS", "lots")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() = nil, want an error for a non-numeric budget")
	}
	if !strings.Contains(err.Error(), "MaxProviderCalls") {
		t.Errorf("error %q should name the field that was wrong", err)
	}
}

func TestMalformedDurationFailsLoudly(t *testing.T) {
	isolate(t)
	t.Setenv(Prefix+"RUN_MAX_WALL_CLOCK", "30 minutes")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() = nil, want an error for an unparseable duration")
	}
}

func TestProviderKeysComeFromEnvironmentOnly(t *testing.T) {
	isolate(t)
	t.Setenv(Prefix+"PROVIDER_KEY_BRAVE", "brave-secret")
	t.Setenv(Prefix+"PROVIDER_KEY_TAVILY", "tavily-secret")
	t.Setenv(Prefix+"PROVIDER_KEY_EMPTY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.Prov.Keys["brave"] != "brave-secret" {
		t.Error("a provider key was not read; provider names must be case-insensitive")
	}
	if _, ok := cfg.Prov.Keys["empty"]; ok {
		t.Error("an empty key must not create an entry; the provider should read as unconfigured")
	}
}

func TestRedactedHidesEverySecret(t *testing.T) {
	isolate(t)
	t.Setenv("DATABASE_URL", "postgres://user:hunter2@db.internal:5432/leads?sslmode=disable")
	t.Setenv(Prefix+"SEARCH_ENDPOINT", "https://api.search.example/v1")
	t.Setenv(Prefix+"SEARCH_API_KEY", "search-secret")
	t.Setenv(Prefix+"PROVIDER_KEY_BRAVE", "brave-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	red := cfg.Redacted()

	joined := red.DB.URL + " " + red.Prov.SearchKey
	for k, v := range red.Prov.Keys {
		joined += " " + k + "=" + v
	}
	for _, secret := range []string{"hunter2", "search-secret", "brave-secret"} {
		if strings.Contains(joined, secret) {
			t.Errorf("Redacted() leaked %q", secret)
		}
	}
	// The operator still needs to see that a key exists, and what it points at.
	if red.Prov.SearchKey == "" {
		t.Error("Redacted() dropped the search key marker; an operator cannot tell unset from hidden")
	}
	if red.Prov.Keys["brave"] == "" {
		t.Error("Redacted() dropped a provider key marker entirely")
	}
	if !strings.Contains(red.DB.URL, "db.internal") {
		t.Error("Redacted() should keep the host so the target is still identifiable")
	}
}

func TestRedactedUnsetSecretIsUnchanged(t *testing.T) {
	isolate(t)
	cfg := Default()
	if got := cfg.Redacted().Prov.SearchKey; got != "" {
		t.Errorf("SearchKey = %q, want empty when it was never set", got)
	}
}

func TestValidateRejectsBadRankingBand(t *testing.T) {
	cfg := Default()
	cfg.Rank.ReviewBand = 0.9
	cfg.Rank.AcceptThreshold = 0.5
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "ReviewBand") {
		t.Errorf("a review band above the accept threshold must be rejected, got %v", err)
	}
}

func TestValidateRejectsPerProviderAboveRunBudget(t *testing.T) {
	cfg := Default()
	cfg.Prov.PerProviderCalls = cfg.Run.MaxProviderCalls + 1
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "PerProviderCalls") {
		t.Errorf("a per-provider cap above the run cap is a contradiction, got %v", err)
	}
}

func TestValidateRejectsPlaintextProviderEndpoints(t *testing.T) {
	cfg := Default()
	cfg.Prov.CertificateEndpoint = "http://crt.sh/"
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("a plaintext endpoint must be rejected, got %v", err)
	}
}

func TestValidateRejectsSearchEndpointWithoutKeyInProduction(t *testing.T) {
	cfg := Default()
	cfg.Env = "production"
	cfg.Prov.SearchEndpoint = "https://api.search.example/v1"
	cfg.Prov.SearchKey = ""
	err := cfg.Validate()
	if err == nil {
		t.Error("a production run must not send unauthenticated requests to a search provider")
	}
	cfg.Prov.SearchKey = "k"
	if err := cfg.Validate(); err != nil {
		t.Errorf("with a key the configuration must validate, got %v", err)
	}
}

func TestValidateRejectsInsecureTLSInProduction(t *testing.T) {
	cfg := Default()
	cfg.Env = "production"
	cfg.Prov.InsecureSkipVerify = true
	if err := cfg.Validate(); err == nil {
		t.Error("skipping TLS verification must not be permitted in production")
	}
	cfg.Env = "development"
	if err := cfg.Validate(); err != nil {
		t.Errorf("skipping TLS verification in development is a local-fixture affordance, got %v", err)
	}
}

func TestValidateRejectsPrivateHostsInProduction(t *testing.T) {
	cfg := Default()
	cfg.Env = "production"
	cfg.Crawl.AllowPrivateHosts = true
	if err := cfg.Validate(); err == nil {
		t.Error("AllowPrivateHosts must not be permitted in production")
	}
}

func TestValidateRejectsBadLanguageTags(t *testing.T) {
	cfg := Default()
	cfg.Query.Languages = []string{"en", "pt-BR", "not a tag"}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "BCP-47") {
		t.Errorf("a malformed language tag must be rejected, got %v", err)
	}
	cfg.Query.Languages = []string{"en", "pt-BR", "zh-Hans", "de"}
	if err := cfg.Validate(); err != nil {
		t.Errorf("well-formed tags must be accepted, got %v", err)
	}
}

func TestValidateRejectsZeroPerHostDelay(t *testing.T) {
	// A zero delay means no politeness at all, which would hammer every site
	// discovery touches.
	cfg := Default()
	cfg.Crawl.PerHostDelay = 0
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "PerHostDelay") {
		t.Errorf("PerHostDelay = 0 must be rejected, got %v", err)
	}
}

func TestValidateSkipsCrawlRulesWhenCrawlDisabled(t *testing.T) {
	cfg := Default()
	cfg.Crawl.Enabled = false
	cfg.Crawl.PerHostDelay = 0
	cfg.Crawl.Concurrency = 0
	if err := cfg.Validate(); err != nil {
		t.Errorf("disabled crawling should not require a valid crawl config, got %v", err)
	}
}

func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	// An operator should fix one boot, not restart five times.
	cfg := Default()
	cfg.Env = "nonsense"
	cfg.Run.Concurrency = -1
	cfg.Rank.AcceptThreshold = 2
	cfg.Prov.UserAgent = ""
	cfg.Crawl.PerHostDelay = 0

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want errors")
	}
	msg := err.Error()
	for _, want := range []string{"Env", "Concurrency", "AcceptThreshold", "UserAgent", "PerHostDelay"} {
		if !strings.Contains(msg, want) {
			t.Errorf("joined error is missing %q:\n%v", want, msg)
		}
	}
}

func TestMergeFileOverridesOnlyWhatItSets(t *testing.T) {
	isolate(t)
	path := filepath.Join(t.TempDir(), "discovery.yaml")
	body := `
env: staging
ranking:
  accept_threshold: 0.9
crawl:
  sitemap_max_urls: 42
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ConfigFileEnv, path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.Env != "staging" {
		t.Errorf("Env = %q, want staging", cfg.Env)
	}
	if cfg.Rank.AcceptThreshold != 0.9 {
		t.Errorf("AcceptThreshold = %v, want 0.9", cfg.Rank.AcceptThreshold)
	}
	if cfg.Crawl.SitemapMaxURLs != 42 {
		t.Errorf("SitemapMaxURLs = %d, want 42", cfg.Crawl.SitemapMaxURLs)
	}
	// Everything the file did not mention keeps its default.
	if cfg.Run.Concurrency != Default().Run.Concurrency {
		t.Errorf("Concurrency = %d, want the default to survive a partial file", cfg.Run.Concurrency)
	}
	if cfg.Run.MaxWallClock != Default().Run.MaxWallClock {
		t.Errorf("MaxWallClock = %v, want the default to survive", cfg.Run.MaxWallClock)
	}
}

func TestEnvironmentBeatsFile(t *testing.T) {
	isolate(t)
	path := filepath.Join(t.TempDir(), "discovery.yaml")
	if err := os.WriteFile(path, []byte("ranking:\n  accept_threshold: 0.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ConfigFileEnv, path)
	t.Setenv(Prefix+"RANK_ACCEPT_THRESHOLD", "0.65")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.Rank.AcceptThreshold != 0.65 {
		t.Errorf("AcceptThreshold = %v, want the environment to win over the file", cfg.Rank.AcceptThreshold)
	}
}

func TestMissingConfigFileIsAnError(t *testing.T) {
	isolate(t)
	t.Setenv(ConfigFileEnv, filepath.Join(t.TempDir(), "absent.yaml"))
	if _, err := Load(); err == nil {
		t.Error("a named but missing config file must be an error, not a silent fallback")
	}
}

func TestExampleConfigParsesAndValidates(t *testing.T) {
	// The documented example is the first thing a new operator copies. If it
	// does not parse, everything else in this package is theoretical.
	isolate(t)
	path := filepath.Join(t.TempDir(), "example.yaml")
	if err := WriteExample(path); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ConfigFileEnv, path)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("the shipped example does not load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the shipped example does not validate: %v", err)
	}
	if cfg.Crawl.RawRetention != 0 {
		t.Errorf("RawRetention = %v, want 0; the example says discovery keeps no bodies", cfg.Crawl.RawRetention)
	}
}

func TestWriteExampleRefusesToClobber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "example.yaml")
	if err := WriteExample(path); err != nil {
		t.Fatal(err)
	}
	if err := WriteExample(path); err == nil {
		t.Error("WriteExample must refuse to overwrite an existing file")
	}
}

func TestDurationsAndSizesSurviveTheFile(t *testing.T) {
	isolate(t)
	path := filepath.Join(t.TempDir(), "d.yaml")
	body := "run:\n  max_wall_clock: 90m\ncrawl:\n  max_bytes: 4096\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	if err := cfg.MergeFile(path); err != nil {
		t.Fatal(err)
	}
	if cfg.Run.MaxWallClock != 90*time.Minute {
		t.Errorf("MaxWallClock = %v, want 90m", cfg.Run.MaxWallClock)
	}
	if cfg.Crawl.MaxBytes != 4096 {
		t.Errorf("MaxBytes = %d, want 4096", cfg.Crawl.MaxBytes)
	}
}
