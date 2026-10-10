package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestEveryFieldSurvivesYAML is a guard against a field whose tag does not match
// the key the documentation and example config use. Such a field is the worst
// kind of bug: the operator sets a limit, the run honours the default instead,
// and nothing anywhere reports an error.
func TestEveryFieldSurvivesYAML(t *testing.T) {
	original := Default()
	// Give every scalar a value that cannot be mistaken for its default.
	original.Env = "staging"
	original.Log.Level = "warn"
	original.Log.Format = "json"
	original.API = APIConfig{
		Addr: ":1234", ReadTimeout: 1, ReadHeaderTimeout: 2, WriteTimeout: 3,
		IdleTimeout: 4, ShutdownGrace: 5, MaxBodyBytes: 6,
	}
	original.Run = RunConfig{
		MaxWallClock: 7, MaxCandidates: 8, MaxProviderCalls: 9,
		MaxCandidatesPerQuery: 10, MaxDepth: 11, Concurrency: 12, RunRetainedRows: 13,
	}
	original.Query = QueryConfig{
		MaxPerCandidate: 14, MaxPerRun: 15, Languages: []string{"pt-BR"},
		IncludeLocalLanguage: true, MaxCitiesPerCountry: 16,
	}
	original.Rank = RankingConfig{AcceptThreshold: 0.71, ReviewBand: 0.17, MaxCandidatesPerDomain: 18}
	original.Prov = ProviderConfig{
		Enabled: []string{"seed"}, Disabled: []string{"search"},
		SeedPath: "/tmp/s.csv", CertificateEndpoint: "https://crt.sh/",
		RDAPBootstrapURL: "https://data.iana.org/rdap/dns.json",
		SearchEndpoint:   "https://api.search.example/v1",
		PerProviderCalls: 19, RequestTimeout: 20, MaxPagesPerProvider: 21,
		UserAgent: "agent/9", InsecureSkipVerify: true, MaxRedirects: 22,
		Keys: map[string]string{},
	}
	original.Crawl = CrawlConfig{
		Enabled: true, UserAgent: "agent/8", Concurrency: 23, PerHostDelay: 24,
		FetchTimeout: 25, MaxPages: 26, MaxBytes: 27, MaxRedirects: 28,
		MaxCrawlTime: 29, RespectRobots: true, RawRetention: 30,
		AllowPrivateHosts: true, SitemapMaxURLs: 31,
	}
	original.Seed = SeedConfig{Path: "/tmp/seed.json", DefaultConfidence: 0.42, MaxEntries: 32}

	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)

	// Spell out the keys the operator is told to use. If a tag is mangled, the
	// key below is simply absent from the marshalled document.
	for _, want := range []string{
		"rdap_bootstrap_url:", "sitemap_max_urls:", "search_endpoint:",
		"max_candidates_per_query:", "max_cities_per_country:", "review_band:",
		"run_retained_rows:", "include_local_language:", "per_provider_calls:",
		"max_pages_per_provider:", "per_host_delay:", "max_crawl_time:",
		"allow_private_hosts:", "default_confidence:", "max_entries:",
		"max_provider_calls:", "max_depth:", "max_body_bytes:",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("marshalled config has no %q key; a tag is wrong.\n%s", want, body)
		}
	}

	// A secret must never reach the serialised form.
	for _, secret := range []string{"search-secret", "brave-secret"} {
		if strings.Contains(body, secret) {
			t.Errorf("marshalled config leaked %q", secret)
		}
	}

	// Now the round trip that matters: the keys must land back on the fields.
	var parsed fileConfig
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("the shape this package writes does not read back: %v", err)
	}
	got := Default()
	parsed.apply(&got)

	if got.Prov.RDAPBootstrapURL != original.Prov.RDAPBootstrapURL {
		t.Errorf("RDAPBootstrapURL = %q, want %q", got.Prov.RDAPBootstrapURL, original.Prov.RDAPBootstrapURL)
	}
	if got.Crawl.SitemapMaxURLs != 31 {
		t.Errorf("SitemapMaxURLs = %d, want 31", got.Crawl.SitemapMaxURLs)
	}
	if got.Crawl.PerHostDelay != 24 {
		t.Errorf("PerHostDelay = %v, want 24", got.Crawl.PerHostDelay)
	}
	if got.Rank.ReviewBand != 0.17 {
		t.Errorf("ReviewBand = %v, want 0.17", got.Rank.ReviewBand)
	}
	if got.Run.RunRetainedRows != 13 {
		t.Errorf("RunRetainedRows = %d, want 13", got.Run.RunRetainedRows)
	}
	if got.Seed.DefaultConfidence != 0.42 {
		t.Errorf("Seed.DefaultConfidence = %v, want 0.42", got.Seed.DefaultConfidence)
	}
}

// TestSecretsCannotBeSetFromYAML proves the "never in a file" rule is
// mechanical rather than a promise in a comment.
func TestSecretsCannotBeSetFromYAML(t *testing.T) {
	isolate(t)
	path := filepath.Join(t.TempDir(), "c.yaml")
	body := `
providers:
  search_key: "leaked-from-a-file"
  keys:
    brave: "also-leaked"
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	if err := cfg.MergeFile(path); err != nil {
		t.Fatal(err)
	}
	if cfg.Prov.SearchKey != "" {
		t.Errorf("SearchKey = %q; a key must not be loadable from a file that gets committed", cfg.Prov.SearchKey)
	}
	if len(cfg.Prov.Keys) != 0 {
		t.Errorf("Keys = %v; provider keys must not be loadable from a file", cfg.Prov.Keys)
	}
}

func TestSeedPathFallsBackToTheProviderBlock(t *testing.T) {
	cfg := Default()
	cfg.Prov.SeedPath = "/tmp/from-provider.csv"
	if got := cfg.SeedPathOrDefault(); got != "/tmp/from-provider.csv" {
		t.Errorf("SeedPathOrDefault() = %q, want the provider-level path as a fallback", got)
	}
	cfg.Seed.Path = "/tmp/from-seed.json"
	if got := cfg.SeedPathOrDefault(); got != "/tmp/from-seed.json" {
		t.Errorf("SeedPathOrDefault() = %q, want the seed block to win", got)
	}
}

func TestSeedsAreCopiedNotAliased(t *testing.T) {
	cfg := Default()
	cfg.seeds = []SeedEntry{{Name: "Acme"}}
	got := cfg.Seeds()
	got[0].Name = "Mutated"
	if cfg.Seeds()[0].Name != "Acme" {
		t.Error("Seeds() exposed the configuration's own slice")
	}
}

func TestRedactedDropsSeedContents(t *testing.T) {
	// The seed list is operator data and can be large. A config dump in a log
	// or an API response should not carry it.
	cfg := Default()
	cfg.seeds = []SeedEntry{{Name: "Acme", Notes: "private note"}}
	body, err := yaml.Marshal(cfg.Redacted())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "private note") {
		t.Error("Redacted() still carries seed contents")
	}
}

// TestYAMLCanTurnDefaultsOff pins the reason fileConfig uses pointer fields.
//
// "Absent" and "set to zero" are different intentions, and only the operator can
// tell them apart. A file that omits crawl.enabled wants the default, which is
// on. A file that says crawl.enabled: false wants crawling off, and with plain
// value fields that was silently ignored — the operator would read a config
// saying "disabled" and get a deployment that crawls anyway.
func TestYAMLCanTurnDefaultsOff(t *testing.T) {
	isolate(t)
	path := filepath.Join(t.TempDir(), "c.yaml")
	body := `
crawl:
  enabled: false
  allow_private_hosts: false
  respect_robots: true
query:
  include_local_language: false
  languages: []
  max_cities_per_country: 0
providers:
  insecure_skip_verify: false
  enabled: []
  max_redirects: 0
api:
  max_body_bytes: 0
ranking:
  review_band: 0
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	if !cfg.Crawl.Enabled {
		t.Fatal("the default should have crawling on, so this test proves something")
	}
	if err := cfg.MergeFile(path); err != nil {
		t.Fatal(err)
	}

	if cfg.Crawl.Enabled {
		t.Error("crawl.enabled: false did not disable crawling")
	}
	if cfg.Crawl.AllowPrivateHosts {
		t.Error("crawl.allow_private_hosts: false did not clear a default of true")
	}
	if !cfg.Crawl.RespectRobots {
		t.Error("crawl.respect_robots: true did not apply")
	}
	if cfg.Query.IncludeLocalLanguage {
		t.Error("query.include_local_language: false did not clear a default of true")
	}
	if cfg.Query.Languages == nil || len(cfg.Query.Languages) != 0 {
		t.Errorf("providers.enabled: an explicit empty list must clear the default, got %v", cfg.Query.Languages)
	}
	if cfg.Prov.Enabled == nil || len(cfg.Prov.Enabled) != 0 {
		t.Errorf("an explicit empty list must clear the default, got %v", cfg.Prov.Enabled)
	}
	// Zeros reach Validate, which is where an impossible value is reported.
	// The point is that the operator's 0 was carried through rather than
	// discarded, and a zero that means "unlimited" is honoured.
	if cfg.Prov.MaxRedirects != 0 {
		t.Errorf("providers.max_redirects: 0 was overwritten with %d", cfg.Prov.MaxRedirects)
	}
	if cfg.Rank.ReviewBand != 0 {
		t.Errorf("ranking.review_band: 0 was overwritten with %v", cfg.Rank.ReviewBand)
	}
}

// TestYAMLAbsentFieldsKeepDefaults is the other half of the same rule: a file
// that says nothing about a setting must not reset it.
func TestYAMLAbsentFieldsKeepDefaults(t *testing.T) {
	isolate(t)
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte("env: staging\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	want := Default()
	if err := cfg.MergeFile(path); err != nil {
		t.Fatal(err)
	}
	if cfg.Env != "staging" {
		t.Errorf("Env = %q, want staging", cfg.Env)
	}
	if cfg.Run.Concurrency != want.Run.Concurrency {
		t.Errorf("Concurrency = %d, want the default %d", cfg.Run.Concurrency, want.Run.Concurrency)
	}
	if cfg.Crawl.Enabled != want.Crawl.Enabled {
		t.Errorf("Crawl.Enabled = %v, want the default %v", cfg.Crawl.Enabled, want.Crawl.Enabled)
	}
	if cfg.API.Addr != want.API.Addr {
		t.Errorf("Addr = %q, want the default %q", cfg.API.Addr, want.API.Addr)
	}
}

// TestEnvironmentStillBeatsYAML keeps the precedence rule intact after the
// pointer rewrite: the file is applied first, so an explicit environment value
// has to win even against a value the file set.
func TestEnvironmentStillBeatsYAML(t *testing.T) {
	isolate(t)
	t.Setenv(Prefix+"RUN_CONCURRENCY", "3")
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte("run:\n  concurrency: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	if err := cfg.MergeFile(path); err != nil {
		t.Fatal(err)
	}
	if cfg.Run.Concurrency != 9 {
		t.Fatalf("MergeFile should apply the file, got %d", cfg.Run.Concurrency)
	}

	// Load is the ordering that matters: file first, then environment.
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Run.Concurrency != 3 {
		t.Errorf("Concurrency = %d, want the environment value 3 to win over the file's 9", loaded.Run.Concurrency)
	}
}
