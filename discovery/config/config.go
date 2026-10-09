// Package config loads and validates discovery's runtime configuration.
//
// Every knob is an environment variable with a DISCOVERY_ prefix, or a YAML file
// for the parts that are lists rather than scalars. The split is deliberate: a
// limit or a timeout belongs in the environment where an operator can change it
// for one run, while a set of seed URLs or a provider allowlist is data that
// belongs in a file that can be reviewed and diffed.
//
// The rule this package enforces beyond parsing is that secrets are never
// returned by Redacted. Provider API keys are read from the environment only;
// there is deliberately no way to put one in a YAML file that gets committed.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	platform "github.com/hmza-hb/lead-intelligence/platform/config"
	"gopkg.in/yaml.v3"
)

// Prefix is prepended to every discovery-specific variable.
const Prefix = "UPVISTA_DISCOVERY_"

// Config is the complete discovery configuration.
type Config struct {
	// Env is inherited from the platform so that log verbosity and behaviour
	// are consistent across the whole system.
	Env   string                  `json:"env" yaml:"env"`
	Log   platform.LogConfig      `json:"log" yaml:"log"`
	DB    platform.DatabaseConfig `json:"-" yaml:"-"`
	API   APIConfig               `json:"api" yaml:"api"`
	Run   RunConfig               `json:"run" yaml:"run"`
	Query QueryConfig             `json:"query" yaml:"query"`
	Rank  RankingConfig           `json:"ranking" yaml:"ranking"`
	Prov  ProviderConfig          `json:"providers" yaml:"providers"`
	Crawl CrawlConfig             `json:"crawl" yaml:"crawl"`
	Seed  SeedConfig              `json:"seed" yaml:"seed"`

	// seeds holds file contents rather than paths so that Validate and
	// Redacted can reason about the whole configuration, and so the loader can
	// report a bad file before anything opens a network connection.
	seeds []SeedEntry
}

// APIConfig controls the discovery HTTP service.
type APIConfig struct {
	Addr              string        `json:"addr" yaml:"addr"`
	ReadTimeout       time.Duration `json:"read_timeout" yaml:"read_timeout"`
	ReadHeaderTimeout time.Duration `json:"read_header_timeout" yaml:"read_header_timeout"`
	WriteTimeout      time.Duration `json:"write_timeout" yaml:"write_timeout"`
	IdleTimeout       time.Duration `json:"idle_timeout" yaml:"idle_timeout"`
	ShutdownGrace     time.Duration `json:"shutdown_grace" yaml:"shutdown_grace"`
	MaxBodyBytes      int64         `json:"max_body_bytes" yaml:"max_body_bytes"`
}

// RunConfig bounds one discovery run. The architecture requires budgets to be
// enforced in code, so each of these becomes a counter the service refuses to
// exceed rather than a suggestion in a runbook.
type RunConfig struct {
	// MaxWallClock bounds a whole run.
	MaxWallClock time.Duration `json:"max_wall_clock" yaml:"max_wall_clock"`
	// MaxCandidates caps how many candidates a run may persist.
	MaxCandidates int `json:"max_candidates" yaml:"max_candidates"`
	// MaxProviderCalls caps outbound calls across all providers, which is the
	// only bound that stops a misconfigured provider from spending money.
	MaxProviderCalls int `json:"max_provider_calls" yaml:"max_provider_calls"`
	// MaxCandidatesPerQuery stops one broad query from flooding the candidate
	// set with the same popular companies.
	MaxCandidatesPerQuery int `json:"max_candidates_per_query" yaml:"max_candidates_per_query"`
	// MaxDepth bounds expansion rounds.
	MaxDepth int `json:"max_depth" yaml:"max_depth"`
	// Concurrency is the number of providers run in parallel.
	Concurrency int `json:"concurrency" yaml:"concurrency"`
	// RunRetainedRows is how many past runs keep their raw evidence. The
	// durable candidate records outlive this.
	RunRetainedRows int `json:"run_retained_rows" yaml:"run_retained_rows"`
}

// QueryConfig controls query generation.
type QueryConfig struct {
	// MaxPerCandidate caps queries generated for a single candidate.
	MaxPerCandidate int `json:"max_per_candidate" yaml:"max_per_candidate"`
	// MaxPerRun caps total queries in a run.
	MaxPerRun int `json:"max_per_run" yaml:"max_per_run"`
	// Languages are the BCP-47 codes to generate localized queries in. An
	// empty list means the candidate's own country only.
	Languages []string `json:"languages" yaml:"languages"`
	// IncludeLocalLanguage generates queries in the country's local language,
	// which is usually where a local market's companies are described.
	IncludeLocalLanguage bool `json:"include_local_language" yaml:"include_local_language"`
	// MaxCitiesPerCountry caps geographic expansion; a country with fifty
	// cities produces more noise than signal past a handful.
	MaxCitiesPerCountry int `json:"max_cities_per_country" yaml:"max_cities_per_country"`
}

// RankingConfig controls the accept threshold.
type RankingConfig struct {
	// AcceptThreshold is the score at or above which a candidate is queued.
	AcceptThreshold float64 `json:"accept_threshold" yaml:"accept_threshold"`
	// ReviewBand is the score band below AcceptThreshold that is kept for
	// human review. Rejecting outright at the threshold would hide the
	// candidates that were nearly good enough, which is exactly what an operator
	// tuning the threshold needs to see.
	ReviewBand float64 `json:"review_band" yaml:"review_band"`
	// MaxCandidatesPerDomain bounds how many candidates may share a
	// registrable domain, so one domain cannot flood the output.
	MaxCandidatesPerDomain int `json:"max_candidates_per_domain" yaml:"max_candidates_per_domain"`
}

// ProviderConfig controls provider behaviour as a group.
type ProviderConfig struct {
	// Enabled lists provider names to run. Empty means every provider that is
	// configured and ready; a name here must exist or the run fails rather
	// than silently discovering nothing.
	Enabled []string `json:"enabled" yaml:"enabled"`
	// Disabled lists provider names to exclude, applied after Enabled.
	Disabled []string `json:"disabled" yaml:"disabled"`
	// SeedPath is a CSV or JSON file of operator-supplied companies.
	SeedPath string `json:"seed_path" yaml:"seed_path"`
	// CertificateEndpoint is the crt.sh JSON API.
	CertificateEndpoint string `json:"certificate_endpoint" yaml:"certificate_endpoint"`
	// RDAPBootstrapURL is the IANA RDAP bootstrap registry.
	RDAPBootstrapURL string `json:"rdap_bootstrap_url" yaml:"rdap_bootstrap_url"`
	// SearchEndpoint and SearchKey enable a web search provider. With no key the
	// provider is registered but reports itself unready and is skipped, so a
	// missing key degrades coverage instead of failing the run.
	SearchEndpoint string `json:"search_endpoint" yaml:"search_endpoint"`
	SearchKey      string `json:"-" yaml:"-"`
	// Keys are provider API keys keyed by provider name. Values are secrets:
	// they are read from the environment and never from YAML, and never appear
	// in Redacted.
	Keys map[string]string `json:"-" yaml:"-"`
	// PerProviderCalls caps each provider independently, so one expensive
	// provider cannot consume the whole run budget.
	PerProviderCalls int `json:"per_provider_calls" yaml:"per_provider_calls"`
	// RequestTimeout bounds a single provider request.
	RequestTimeout time.Duration `json:"request_timeout" yaml:"request_timeout"`
	// MaxPagesPerProvider caps pagination, so a provider whose "next" link
	// never ends cannot loop forever.
	MaxPagesPerProvider int `json:"max_pages_per_provider" yaml:"max_pages_per_provider"`
	// UserAgent identifies discovery to the sites and APIs it queries.
	UserAgent string `json:"user_agent" yaml:"user_agent"`
	// MaxRedirects and InsecureSkipVerify are exposed but default safe.
	InsecureSkipVerify bool `json:"insecure_skip_verify" yaml:"insecure_skip_verify"`
	MaxRedirects       int  `json:"max_redirects" yaml:"max_redirects"`
}

// CrawlConfig configures the crawler calls discovery makes. Discovery only uses
// the crawler to read sitemaps and follow a small number of known-good pages; it
// is not a general-purpose crawl, so the defaults here are much tighter than
// the crawler's own.
type CrawlConfig struct {
	Enabled           bool          `json:"enabled" yaml:"enabled"`
	UserAgent         string        `json:"user_agent" yaml:"user_agent"`
	Concurrency       int           `json:"concurrency" yaml:"concurrency"`
	PerHostDelay      time.Duration `json:"per_host_delay" yaml:"per_host_delay"`
	FetchTimeout      time.Duration `json:"fetch_timeout" yaml:"fetch_timeout"`
	MaxPages          int           `json:"max_pages" yaml:"max_pages"`
	MaxBytes          int64         `json:"max_bytes" yaml:"max_bytes"`
	MaxRedirects      int           `json:"max_redirects" yaml:"max_redirects"`
	MaxCrawlTime      time.Duration `json:"max_crawl_time" yaml:"max_crawl_time"`
	RespectRobots     bool          `json:"respect_robots" yaml:"respect_robots"`
	RawRetention      time.Duration `json:"raw_retention" yaml:"raw_retention"`
	AllowPrivateHosts bool          `json:"allow_private_hosts" yaml:"allow_private_hosts"`
	// SitemapMaxURLs caps how many <loc> entries one sitemap may contribute, so
	// a 200k-URL sitemap cannot consume a whole run.
	SitemapMaxURLs int `json:"sitemap_max_urls" yaml:"sitemap_max_urls"`
}

// SeedConfig controls operator-supplied companies.
type SeedConfig struct {
	// Path is the seed file. Empty means no seed provider.
	Path string `json:"path" yaml:"path"`
	// DefaultConfidence is the confidence assigned to a seed with no
	// confidence column of its own.
	DefaultConfidence float64 `json:"default_confidence" yaml:"default_confidence"`
	// MaxEntries caps the file. A seed list is meant to name companies the
	// operator already knows about, not to be a bulk import.
	MaxEntries int `json:"max_entries" yaml:"max_entries"`
}

// SeedEntry is one line of a seed file.
type SeedEntry struct {
	// Name is the company name as the operator wrote it.
	Name string `json:"name" yaml:"name"`
	// Domain is optional. A seed with only a name is a legitimate discovery:
	// the expansion stage is what looks for the domain.
	Domain string `json:"domain" yaml:"domain"`
	// Country is an ISO 3166-1 alpha-2 code.
	Country string `json:"country" yaml:"country"`
	// Industry is free text.
	Industry string `json:"industry" yaml:"industry"`
	// EmployeeHint is a size band as text.
	EmployeeHint string `json:"employee_hint" yaml:"employee_hint"`
	// Notes are free text carried into the candidate description.
	Notes string `json:"notes" yaml:"notes"`
	// Confidence overrides the default when set, in [0,1].
	Confidence *float64 `json:"confidence" yaml:"confidence"`
}

// Default returns a configuration that runs locally with only DATABASE_URL set.
func Default() Config {
	return Config{
		Env: "development",
		Log: platform.LogConfig{Level: "info", Format: "text"},
		DB: platform.DatabaseConfig{
			MaxConns:         8,
			MinConns:         1,
			ConnectTimeout:   10 * time.Second,
			StatementTimeout: 30 * time.Second,
			HealthCheck:      30 * time.Second,
		},
		API: APIConfig{
			Addr:              ":8081",
			ReadTimeout:       15 * time.Second,
			ReadHeaderTimeout: 5 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       90 * time.Second,
			ShutdownGrace:     20 * time.Second,
			MaxBodyBytes:      1 << 20,
		},
		Run: RunConfig{
			MaxWallClock:          30 * time.Minute,
			MaxCandidates:         100_000,
			MaxProviderCalls:      20_000,
			MaxCandidatesPerQuery: 50,
			MaxDepth:              2,
			Concurrency:           4,
			RunRetainedRows:       10,
		},
		Query: QueryConfig{
			MaxPerCandidate:      12,
			MaxPerRun:            5_000,
			MaxCitiesPerCountry:  3,
			IncludeLocalLanguage: true,
		},
		Rank: RankingConfig{
			AcceptThreshold:        0.55,
			ReviewBand:             0.35,
			MaxCandidatesPerDomain: 5,
		},
		Prov: ProviderConfig{
			CertificateEndpoint: "https://crt.sh/",
			RDAPBootstrapURL:    "https://data.iana.org/rdap/dns.json",
			PerProviderCalls:    5_000,
			RequestTimeout:      30 * time.Second,
			MaxPagesPerProvider: 20,
			UserAgent:           "UpvistaDiscovery/1.0 (+https://upvista.example/bot)",
			MaxRedirects:        3,
			Keys:                map[string]string{},
		},
		Crawl: CrawlConfig{
			Enabled:        true,
			UserAgent:      "UpvistaDiscovery/1.0 (+https://upvista.example/bot)",
			Concurrency:    2,
			PerHostDelay:   2 * time.Second,
			FetchTimeout:   20 * time.Second,
			MaxPages:       25,
			MaxBytes:       1 << 20,
			MaxRedirects:   3,
			MaxCrawlTime:   2 * time.Minute,
			RespectRobots:  true,
			RawRetention:   0, // discovery keeps links, not bodies
			SitemapMaxURLs: 500,
		},
		Seed: SeedConfig{
			DefaultConfidence: 0.9,
			MaxEntries:        50_000,
		},
	}
}

// Load reads the configuration from a YAML file if one is named, then from the
// environment.
//
// The order is the contract: the file is the reviewed baseline and the
// environment is the deployment's adjustment, so the environment is applied last
// and wins. Applying them the other way round means an operator's
// UPVISTA_DISCOVERY_RUN_MAX_CANDIDATES is silently ignored whenever a config
// file happens to set the same key, which is the kind of bug that only shows up
// in production.
//
// Every problem is reported at once. An operator should fix one boot, not
// discover the next error after restarting.
func Load() (Config, error) {
	cfg := Default()
	var errs []error

	// The file goes on first. Its seed path is resolved after the environment
	// is read, so a MaxEntries limit set in the environment still applies to a
	// seed file named in the file.
	configFile := envString("CONFIG_FILE", "")
	var file fileConfig
	if configFile != "" {
		data, err := os.ReadFile(configFile)
		if err != nil {
			errs = append(errs, fmt.Errorf("config: read %s: %w", configFile, err))
		} else if err := yaml.Unmarshal(data, &file); err != nil {
			errs = append(errs, fmt.Errorf("config: parse %s: %w", configFile, err))
		} else {
			file.apply(&cfg)
		}
	}

	cfg.Env = envStringAny([]string{"ENV"}, cfg.Env)
	cfg.Log.Level = envStringAny([]string{"LOG_LEVEL"}, cfg.Log.Level)
	cfg.Log.Format = envStringAny([]string{"LOG_FORMAT"}, cfg.Log.Format)
	cfg.DB.URL = envStringAny([]string{"DATABASE_URL"}, os.Getenv("DATABASE_URL"))
	cfg.DB.MaxConns = int32(envInt64("DB_MAX_CONNS", int64(cfg.DB.MaxConns)))
	cfg.DB.MinConns = int32(envInt64("DB_MIN_CONNS", int64(cfg.DB.MinConns)))
	cfg.DB.ConnectTimeout = envDuration("DB_CONNECT_TIMEOUT", cfg.DB.ConnectTimeout)
	cfg.DB.StatementTimeout = envDuration("DB_STATEMENT_TIMEOUT", cfg.DB.StatementTimeout)
	cfg.DB.HealthCheck = envDuration("DB_HEALTH_CHECK_PERIOD", cfg.DB.HealthCheck)

	cfg.API.Addr = envStringAny([]string{"HTTP_ADDR"}, cfg.API.Addr)
	cfg.API.ReadTimeout = envDuration("HTTP_READ_TIMEOUT", cfg.API.ReadTimeout)
	cfg.API.ReadHeaderTimeout = envDuration("HTTP_READ_HEADER_TIMEOUT", cfg.API.ReadHeaderTimeout)
	cfg.API.WriteTimeout = envDuration("HTTP_WRITE_TIMEOUT", cfg.API.WriteTimeout)
	cfg.API.IdleTimeout = envDuration("HTTP_IDLE_TIMEOUT", cfg.API.IdleTimeout)
	cfg.API.ShutdownGrace = envDuration("HTTP_SHUTDOWN_GRACE", cfg.API.ShutdownGrace)
	cfg.API.MaxBodyBytes = envInt64("HTTP_MAX_BODY_BYTES", cfg.API.MaxBodyBytes)

	cfg.Run.MaxWallClock = envDuration("RUN_MAX_WALL_CLOCK", cfg.Run.MaxWallClock)
	cfg.Run.MaxCandidates = envInt("RUN_MAX_CANDIDATES", cfg.Run.MaxCandidates)
	cfg.Run.MaxProviderCalls = envInt("RUN_MAX_PROVIDER_CALLS", cfg.Run.MaxProviderCalls)
	cfg.Run.MaxCandidatesPerQuery = envInt("RUN_MAX_CANDIDATES_PER_QUERY", cfg.Run.MaxCandidatesPerQuery)
	cfg.Run.MaxDepth = envInt("RUN_MAX_DEPTH", cfg.Run.MaxDepth)
	cfg.Run.Concurrency = envInt("RUN_CONCURRENCY", cfg.Run.Concurrency)
	cfg.Run.RunRetainedRows = envInt("RUN_RETAINED_ROWS", cfg.Run.RunRetainedRows)

	cfg.Query.MaxPerCandidate = envInt("QUERY_MAX_PER_CANDIDATE", cfg.Query.MaxPerCandidate)
	cfg.Query.MaxPerRun = envInt("QUERY_MAX_PER_RUN", cfg.Query.MaxPerRun)
	cfg.Query.MaxCitiesPerCountry = envInt("QUERY_MAX_CITIES_PER_COUNTRY", cfg.Query.MaxCitiesPerCountry)
	cfg.Query.Languages = envList("QUERY_LANGUAGES", cfg.Query.Languages)
	cfg.Query.IncludeLocalLanguage = envBool("QUERY_INCLUDE_LOCAL_LANGUAGE", cfg.Query.IncludeLocalLanguage)

	cfg.Rank.AcceptThreshold = envFloat("RANK_ACCEPT_THRESHOLD", cfg.Rank.AcceptThreshold)
	cfg.Rank.ReviewBand = envFloat("RANK_REVIEW_BAND", cfg.Rank.ReviewBand)
	cfg.Rank.MaxCandidatesPerDomain = envInt("RANK_MAX_CANDIDATES_PER_DOMAIN", cfg.Rank.MaxCandidatesPerDomain)

	cfg.Prov.Enabled = envList("PROVIDERS_ENABLED", cfg.Prov.Enabled)
	cfg.Prov.Disabled = envList("PROVIDERS_DISABLED", cfg.Prov.Disabled)
	cfg.Prov.SeedPath = envString("SEED_PATH", cfg.Prov.SeedPath)
	cfg.Prov.CertificateEndpoint = envString("CERT_ENDPOINT", cfg.Prov.CertificateEndpoint)
	cfg.Prov.RDAPBootstrapURL = envString("RDAP_BOOTSTRAP_URL", cfg.Prov.RDAPBootstrapURL)
	cfg.Prov.SearchEndpoint = envString("SEARCH_ENDPOINT", cfg.Prov.SearchEndpoint)
	cfg.Prov.SearchKey = envStringAny([]string{"SEARCH_API_KEY"}, cfg.Prov.SearchKey)
	cfg.Prov.PerProviderCalls = envInt("PROVIDER_MAX_CALLS", cfg.Prov.PerProviderCalls)
	cfg.Prov.RequestTimeout = envDuration("PROVIDER_REQUEST_TIMEOUT", cfg.Prov.RequestTimeout)
	cfg.Prov.MaxPagesPerProvider = envInt("PROVIDER_MAX_PAGES", cfg.Prov.MaxPagesPerProvider)
	cfg.Prov.UserAgent = envString("USER_AGENT", cfg.Prov.UserAgent)
	cfg.Prov.InsecureSkipVerify = envBool("PROVIDER_INSECURE_SKIP_VERIFY", cfg.Prov.InsecureSkipVerify)
	cfg.Prov.MaxRedirects = envInt("PROVIDER_MAX_REDIRECTS", cfg.Prov.MaxRedirects)

	// Provider keys come from the environment only. There is no YAML path to a
	// secret on purpose: a file is committed, an environment variable is not.
	for _, env := range os.Environ() {
		name, value, ok := strings.Cut(env, "=")
		if !ok || value == "" {
			continue
		}
		if after, found := strings.CutPrefix(name, Prefix+"PROVIDER_KEY_"); found {
			cfg.Prov.Keys[strings.ToLower(after)] = value
		}
	}

	cfg.Crawl.Enabled = envBool("CRAWL_ENABLED", cfg.Crawl.Enabled)
	cfg.Crawl.UserAgent = envString("CRAWL_USER_AGENT", cfg.Crawl.UserAgent)
	cfg.Crawl.Concurrency = envInt("CRAWL_CONCURRENCY", cfg.Crawl.Concurrency)
	cfg.Crawl.PerHostDelay = envDuration("CRAWL_PER_HOST_DELAY", cfg.Crawl.PerHostDelay)
	cfg.Crawl.FetchTimeout = envDuration("CRAWL_FETCH_TIMEOUT", cfg.Crawl.FetchTimeout)
	cfg.Crawl.MaxPages = envInt("CRAWL_MAX_PAGES", cfg.Crawl.MaxPages)
	cfg.Crawl.MaxBytes = envInt64("CRAWL_MAX_BYTES", cfg.Crawl.MaxBytes)
	cfg.Crawl.MaxRedirects = envInt("CRAWL_MAX_REDIRECTS", cfg.Crawl.MaxRedirects)
	cfg.Crawl.MaxCrawlTime = envDuration("CRAWL_MAX_TIME", cfg.Crawl.MaxCrawlTime)
	cfg.Crawl.RespectRobots = envBool("CRAWL_RESPECT_ROBOTS", cfg.Crawl.RespectRobots)
	cfg.Crawl.RawRetention = envDuration("CRAWL_RAW_RETENTION", cfg.Crawl.RawRetention)
	cfg.Crawl.AllowPrivateHosts = envBool("CRAWL_ALLOW_PRIVATE_HOSTS", cfg.Crawl.AllowPrivateHosts)
	cfg.Crawl.SitemapMaxURLs = envInt("CRAWL_SITEMAP_MAX_URLS", cfg.Crawl.SitemapMaxURLs)

	cfg.Seed.Path = envString("SEED_PATH", cfg.Seed.Path)
	cfg.Seed.DefaultConfidence = envFloat("SEED_DEFAULT_CONFIDENCE", cfg.Seed.DefaultConfidence)
	cfg.Seed.MaxEntries = envInt("SEED_MAX_ENTRIES", cfg.Seed.MaxEntries)

	// Seeds are read last, so the entry cap from the environment or the file
	// applies to whatever file is named.
	seedPath := cfg.SeedPathOrDefault()
	if seedPath != "" && len(errs) == 0 {
		entries, err := LoadSeeds(seedPath, cfg.Seed.MaxEntries)
		if err != nil {
			errs = append(errs, err)
		} else {
			cfg.seeds = entries
		}
	}

	if err := cfg.Validate(); err != nil {
		errs = append(errs, err)
	}
	return cfg, errors.Join(errs...)
}

// MergeFile overlays a YAML file onto the configuration. Only non-zero scalar
// fields in the file override what is already set, so a small file holding two
// values does not reset every other default to zero.
func (c *Config) MergeFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: read %s: %w", path, err)
	}
	var file fileConfig
	if err := yaml.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("config: parse %s: %w", path, err)
	}
	file.apply(c)

	if file.Seed != nil && file.Seed.Path != nil {
		c.Seed.Path = *file.Seed.Path
	}
	if path := c.SeedPathOrDefault(); path != "" {
		entries, err := LoadSeeds(path, c.Seed.MaxEntries)
		if err != nil {
			return fmt.Errorf("config: seed file %s: %w", path, err)
		}
		c.seeds = entries
	}
	return nil
}

// fileConfig is the YAML surface.
//
// Every field is a pointer, and that is the whole point of the type. "Absent" and
// "set to zero" are different intentions and only the operator can tell them
// apart: a config file that omits run.concurrency wants the default, while one
// that says run.concurrency: 0 wants a run that does nothing. With plain value
// fields the second is indistinguishable from the first, so `crawl: {enabled:
// false}` silently leaves crawling switched on and a file can never turn a
// default off. A pointer is nil when the key is absent and non-nil whenever the
// operator wrote one, so apply can honour the intent either way.
type fileConfig struct {
	Env   string     `yaml:"env"`
	Log   *fileLog   `yaml:"log"`
	API   *fileAPI   `yaml:"api"`
	Run   *fileRun   `yaml:"run"`
	Query *fileQuery `yaml:"query"`
	Rank  *fileRank  `yaml:"ranking"`
	Prov  *fileProv  `yaml:"providers"`
	Crawl *fileCrawl `yaml:"crawl"`
	Seed  *fileSeed  `yaml:"seed"`
}

type fileLog struct {
	Level  *string `yaml:"level"`
	Format *string `yaml:"format"`
}

type fileAPI struct {
	Addr              *string        `yaml:"addr"`
	ReadTimeout       *time.Duration `yaml:"read_timeout"`
	ReadHeaderTimeout *time.Duration `yaml:"read_header_timeout"`
	WriteTimeout      *time.Duration `yaml:"write_timeout"`
	IdleTimeout       *time.Duration `yaml:"idle_timeout"`
	ShutdownGrace     *time.Duration `yaml:"shutdown_grace"`
	MaxBodyBytes      *int64         `yaml:"max_body_bytes"`
}

type fileRun struct {
	MaxWallClock          *time.Duration `yaml:"max_wall_clock"`
	MaxCandidates         *int           `yaml:"max_candidates"`
	MaxProviderCalls      *int           `yaml:"max_provider_calls"`
	MaxCandidatesPerQuery *int           `yaml:"max_candidates_per_query"`
	MaxDepth              *int           `yaml:"max_depth"`
	Concurrency           *int           `yaml:"concurrency"`
	RunRetainedRows       *int           `yaml:"run_retained_rows"`
}

type fileQuery struct {
	MaxPerCandidate      *int      `yaml:"max_per_candidate"`
	MaxPerRun            *int      `yaml:"max_per_run"`
	Languages            *[]string `yaml:"languages"`
	IncludeLocalLanguage *bool     `yaml:"include_local_language"`
	MaxCitiesPerCountry  *int      `yaml:"max_cities_per_country"`
}

type fileRank struct {
	AcceptThreshold        *float64 `yaml:"accept_threshold"`
	ReviewBand             *float64 `yaml:"review_band"`
	MaxCandidatesPerDomain *int     `yaml:"max_candidates_per_domain"`
}

type fileProv struct {
	Enabled             *[]string      `yaml:"enabled"`
	Disabled            *[]string      `yaml:"disabled"`
	SeedPath            *string        `yaml:"seed_path"`
	CertificateEndpoint *string        `yaml:"certificate_endpoint"`
	RDAPBootstrapURL    *string        `yaml:"rdap_bootstrap_url"`
	SearchEndpoint      *string        `yaml:"search_endpoint"`
	PerProviderCalls    *int           `yaml:"per_provider_calls"`
	RequestTimeout      *time.Duration `yaml:"request_timeout"`
	MaxPagesPerProvider *int           `yaml:"max_pages_per_provider"`
	UserAgent           *string        `yaml:"user_agent"`
	InsecureSkipVerify  *bool          `yaml:"insecure_skip_verify"`
	MaxRedirects        *int           `yaml:"max_redirects"`
}

type fileCrawl struct {
	Enabled           *bool          `yaml:"enabled"`
	UserAgent         *string        `yaml:"user_agent"`
	Concurrency       *int           `yaml:"concurrency"`
	PerHostDelay      *time.Duration `yaml:"per_host_delay"`
	FetchTimeout      *time.Duration `yaml:"fetch_timeout"`
	MaxPages          *int           `yaml:"max_pages"`
	MaxBytes          *int64         `yaml:"max_bytes"`
	MaxRedirects      *int           `yaml:"max_redirects"`
	MaxCrawlTime      *time.Duration `yaml:"max_crawl_time"`
	RespectRobots     *bool          `yaml:"respect_robots"`
	RawRetention      *time.Duration `yaml:"raw_retention"`
	AllowPrivateHosts *bool          `yaml:"allow_private_hosts"`
	SitemapMaxURLs    *int           `yaml:"sitemap_max_urls"`
}

type fileSeed struct {
	Path              *string  `yaml:"path"`
	DefaultConfidence *float64 `yaml:"default_confidence"`
	MaxEntries        *int     `yaml:"max_entries"`
}

// apply copies every field the operator actually wrote, and leaves everything
// else at the value the defaults or the environment already established.
func (f fileConfig) apply(c *Config) {
	if f.Env != "" {
		c.Env = f.Env
	}
	if f.Log != nil {
		if f.Log.Level != nil {
			c.Log.Level = *f.Log.Level
		}
		if f.Log.Format != nil {
			c.Log.Format = *f.Log.Format
		}
	}
	if f.API != nil {
		setString(&c.API.Addr, f.API.Addr)
		setDuration(&c.API.ReadTimeout, f.API.ReadTimeout)
		setDuration(&c.API.ReadHeaderTimeout, f.API.ReadHeaderTimeout)
		setDuration(&c.API.WriteTimeout, f.API.WriteTimeout)
		setDuration(&c.API.IdleTimeout, f.API.IdleTimeout)
		setDuration(&c.API.ShutdownGrace, f.API.ShutdownGrace)
		setInt64(&c.API.MaxBodyBytes, f.API.MaxBodyBytes)
	}
	if f.Run != nil {
		setDuration(&c.Run.MaxWallClock, f.Run.MaxWallClock)
		setInt(&c.Run.MaxCandidates, f.Run.MaxCandidates)
		setInt(&c.Run.MaxProviderCalls, f.Run.MaxProviderCalls)
		setInt(&c.Run.MaxCandidatesPerQuery, f.Run.MaxCandidatesPerQuery)
		setInt(&c.Run.MaxDepth, f.Run.MaxDepth)
		setInt(&c.Run.Concurrency, f.Run.Concurrency)
		setInt(&c.Run.RunRetainedRows, f.Run.RunRetainedRows)
	}
	if f.Query != nil {
		setInt(&c.Query.MaxPerCandidate, f.Query.MaxPerCandidate)
		setInt(&c.Query.MaxPerRun, f.Query.MaxPerRun)
		setList(&c.Query.Languages, f.Query.Languages)
		setBool(&c.Query.IncludeLocalLanguage, f.Query.IncludeLocalLanguage)
		setInt(&c.Query.MaxCitiesPerCountry, f.Query.MaxCitiesPerCountry)
	}
	if f.Rank != nil {
		setFloat(&c.Rank.AcceptThreshold, f.Rank.AcceptThreshold)
		setFloat(&c.Rank.ReviewBand, f.Rank.ReviewBand)
		setInt(&c.Rank.MaxCandidatesPerDomain, f.Rank.MaxCandidatesPerDomain)
	}
	if f.Prov != nil {
		setList(&c.Prov.Enabled, f.Prov.Enabled)
		setList(&c.Prov.Disabled, f.Prov.Disabled)
		setString(&c.Prov.SeedPath, f.Prov.SeedPath)
		setString(&c.Prov.CertificateEndpoint, f.Prov.CertificateEndpoint)
		setString(&c.Prov.RDAPBootstrapURL, f.Prov.RDAPBootstrapURL)
		setString(&c.Prov.SearchEndpoint, f.Prov.SearchEndpoint)
		setInt(&c.Prov.PerProviderCalls, f.Prov.PerProviderCalls)
		setDuration(&c.Prov.RequestTimeout, f.Prov.RequestTimeout)
		setInt(&c.Prov.MaxPagesPerProvider, f.Prov.MaxPagesPerProvider)
		setString(&c.Prov.UserAgent, f.Prov.UserAgent)
		setBool(&c.Prov.InsecureSkipVerify, f.Prov.InsecureSkipVerify)
		setInt(&c.Prov.MaxRedirects, f.Prov.MaxRedirects)
	}
	if f.Crawl != nil {
		setBool(&c.Crawl.Enabled, f.Crawl.Enabled)
		setString(&c.Crawl.UserAgent, f.Crawl.UserAgent)
		setInt(&c.Crawl.Concurrency, f.Crawl.Concurrency)
		setDuration(&c.Crawl.PerHostDelay, f.Crawl.PerHostDelay)
		setDuration(&c.Crawl.FetchTimeout, f.Crawl.FetchTimeout)
		setInt(&c.Crawl.MaxPages, f.Crawl.MaxPages)
		setInt64(&c.Crawl.MaxBytes, f.Crawl.MaxBytes)
		setInt(&c.Crawl.MaxRedirects, f.Crawl.MaxRedirects)
		setDuration(&c.Crawl.MaxCrawlTime, f.Crawl.MaxCrawlTime)
		setBool(&c.Crawl.RespectRobots, f.Crawl.RespectRobots)
		setDuration(&c.Crawl.RawRetention, f.Crawl.RawRetention)
		setBool(&c.Crawl.AllowPrivateHosts, f.Crawl.AllowPrivateHosts)
		setInt(&c.Crawl.SitemapMaxURLs, f.Crawl.SitemapMaxURLs)
	}
	if f.Seed != nil {
		setString(&c.Seed.Path, f.Seed.Path)
		setFloat(&c.Seed.DefaultConfidence, f.Seed.DefaultConfidence)
		setInt(&c.Seed.MaxEntries, f.Seed.MaxEntries)
	}
}

func setString(dst *string, v *string) {
	if v != nil {
		*dst = *v
	}
}

func setBool(dst *bool, v *bool) {
	if v != nil {
		*dst = *v
	}
}

func setInt(dst *int, v *int) {
	if v != nil {
		*dst = *v
	}
}

func setInt64(dst *int64, v *int64) {
	if v != nil {
		*dst = *v
	}
}

func setFloat(dst *float64, v *float64) {
	if v != nil {
		*dst = *v
	}
}

func setDuration(dst *time.Duration, v *time.Duration) {
	if v != nil {
		*dst = *v
	}
}

// setList assigns the slice even when the operator wrote an empty list, because
// an empty list is how a config file says "no countries", "no languages", which
// is not the same as leaving the default in place.
func setList(dst *[]string, v *[]string) {
	if v != nil {
		*dst = *v
	}
}

// Validate reports every problem at once.
func (c Config) Validate() error {
	var errs []error

	switch c.Env {
	case "development", "test", "staging", "production":
	default:
		errs = append(errs, fmt.Errorf("config: Env %q is not one of development, test, staging, production", c.Env))
	}

	if !strings.HasPrefix(c.API.Addr, ":") && !strings.Contains(c.API.Addr, ":") {
		errs = append(errs, fmt.Errorf("config: API.Addr %q must be host:port", c.API.Addr))
	}
	if c.API.MaxBodyBytes <= 0 {
		errs = append(errs, fmt.Errorf("config: API.MaxBodyBytes must be positive"))
	}

	if c.Run.MaxWallClock <= 0 {
		errs = append(errs, fmt.Errorf("config: Run.MaxWallClock must be positive"))
	}
	if c.Run.MaxCandidates <= 0 {
		errs = append(errs, fmt.Errorf("config: Run.MaxCandidates must be positive"))
	}
	if c.Run.MaxProviderCalls <= 0 {
		errs = append(errs, fmt.Errorf("config: Run.MaxProviderCalls must be positive"))
	}
	if c.Run.MaxCandidatesPerQuery <= 0 {
		errs = append(errs, fmt.Errorf("config: Run.MaxCandidatesPerQuery must be positive"))
	}
	if c.Run.MaxDepth < 0 {
		errs = append(errs, fmt.Errorf("config: Run.MaxDepth must not be negative"))
	}
	if c.Run.Concurrency <= 0 {
		errs = append(errs, fmt.Errorf("config: Run.Concurrency must be positive"))
	}

	if c.Query.MaxPerCandidate <= 0 {
		errs = append(errs, fmt.Errorf("config: Query.MaxPerCandidate must be positive"))
	}
	if c.Query.MaxPerRun <= 0 {
		errs = append(errs, fmt.Errorf("config: Query.MaxPerRun must be positive"))
	}
	if c.Query.MaxPerCandidate > c.Query.MaxPerRun {
		errs = append(errs, fmt.Errorf("config: Query.MaxPerCandidate %d exceeds Query.MaxPerRun %d",
			c.Query.MaxPerCandidate, c.Query.MaxPerRun))
	}
	if c.Query.MaxCitiesPerCountry < 0 {
		errs = append(errs, fmt.Errorf("config: Query.MaxCitiesPerCountry must not be negative"))
	}
	for _, lang := range c.Query.Languages {
		if !isLanguageTag(lang) {
			errs = append(errs, fmt.Errorf("config: Query.Languages contains %q, which is not a BCP-47 tag", lang))
		}
	}

	if err := c.Rank.validate(); err != nil {
		errs = append(errs, err)
	}

	if c.Prov.PerProviderCalls <= 0 {
		errs = append(errs, fmt.Errorf("config: Prov.PerProviderCalls must be positive"))
	}
	if c.Prov.PerProviderCalls > c.Run.MaxProviderCalls {
		errs = append(errs, fmt.Errorf("config: Prov.PerProviderCalls %d exceeds Run.MaxProviderCalls %d",
			c.Prov.PerProviderCalls, c.Run.MaxProviderCalls))
	}
	if c.Prov.RequestTimeout <= 0 {
		errs = append(errs, fmt.Errorf("config: Prov.RequestTimeout must be positive"))
	}
	if c.Prov.MaxPagesPerProvider <= 0 {
		errs = append(errs, fmt.Errorf("config: Prov.MaxPagesPerProvider must be positive"))
	}
	if c.Prov.MaxRedirects < 0 {
		errs = append(errs, fmt.Errorf("config: Prov.MaxRedirects must not be negative"))
	}
	if c.Prov.UserAgent == "" {
		// Not cosmetic. A provider that identifies itself as nothing gets
		// blocked, and the operator has no way to see the cause.
		errs = append(errs, fmt.Errorf("config: Prov.UserAgent is required; identify yourself to the APIs you call"))
	}
	if c.Prov.InsecureSkipVerify && c.Env == "production" {
		errs = append(errs, fmt.Errorf("config: Prov.InsecureSkipVerify must not be enabled in production"))
	}
	if c.Prov.CertificateEndpoint != "" {
		if err := requireHTTPS(c.Prov.CertificateEndpoint, "Prov.CertificateEndpoint"); err != nil {
			errs = append(errs, err)
		}
	}
	if c.Prov.RDAPBootstrapURL != "" {
		if err := requireHTTPS(c.Prov.RDAPBootstrapURL, "Prov.RDAPBootstrapURL"); err != nil {
			errs = append(errs, err)
		}
	}
	if c.Prov.SearchEndpoint != "" {
		if err := requireHTTPS(c.Prov.SearchEndpoint, "Prov.SearchEndpoint"); err != nil {
			errs = append(errs, err)
		}
		if c.Prov.SearchKey == "" && c.Env == "production" {
			errs = append(errs, fmt.Errorf("config: Prov.SearchEndpoint is set but no key is configured; a production run would send unauthenticated requests and be blocked"))
		}
	}
	if c.Prov.SearchKey != "" && c.Prov.SearchEndpoint == "" {
		errs = append(errs, fmt.Errorf("config: a search key is set but Prov.SearchEndpoint is empty"))
	}

	if err := c.Crawl.validate(c.Env); err != nil {
		errs = append(errs, err)
	}

	if c.Seed.DefaultConfidence < 0 || c.Seed.DefaultConfidence > 1 {
		errs = append(errs, fmt.Errorf("config: Seed.DefaultConfidence %v must be in [0,1]", c.Seed.DefaultConfidence))
	}
	if c.Seed.MaxEntries <= 0 {
		errs = append(errs, fmt.Errorf("config: Seed.MaxEntries must be positive"))
	}
	for i, e := range c.seeds {
		if err := e.validate(i); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (r RankingConfig) validate() error {
	var errs []error
	if r.AcceptThreshold <= 0 || r.AcceptThreshold > 1 {
		errs = append(errs, fmt.Errorf("config: Rank.AcceptThreshold %v must be in (0,1]", r.AcceptThreshold))
	}
	if r.ReviewBand < 0 || r.ReviewBand > 1 {
		errs = append(errs, fmt.Errorf("config: Rank.ReviewBand %v must be in [0,1]", r.ReviewBand))
	}
	if r.ReviewBand > r.AcceptThreshold {
		// A review band above the accept threshold would mean reviewing
		// candidates that were already accepted, which is nonsense.
		errs = append(errs, fmt.Errorf("config: Rank.ReviewBand %v must not exceed Rank.AcceptThreshold %v",
			r.ReviewBand, r.AcceptThreshold))
	}
	if r.MaxCandidatesPerDomain <= 0 {
		errs = append(errs, fmt.Errorf("config: Rank.MaxCandidatesPerDomain must be positive"))
	}
	return errors.Join(errs...)
}

func (c CrawlConfig) validate(env string) error {
	var errs []error
	if !c.Enabled {
		return nil
	}
	if c.Concurrency <= 0 {
		errs = append(errs, fmt.Errorf("config: Crawl.Concurrency must be positive"))
	}
	if c.PerHostDelay <= 0 {
		errs = append(errs, fmt.Errorf("config: Crawl.PerHostDelay must be positive; zero would hammer every site"))
	}
	if c.FetchTimeout <= 0 {
		errs = append(errs, fmt.Errorf("config: Crawl.FetchTimeout must be positive"))
	}
	if c.MaxPages <= 0 {
		errs = append(errs, fmt.Errorf("config: Crawl.MaxPages must be positive"))
	}
	if c.MaxBytes <= 0 {
		errs = append(errs, fmt.Errorf("config: Crawl.MaxBytes must be positive"))
	}
	if c.MaxRedirects < 0 {
		errs = append(errs, fmt.Errorf("config: Crawl.MaxRedirects must not be negative"))
	}
	if c.MaxCrawlTime <= 0 {
		errs = append(errs, fmt.Errorf("config: Crawl.MaxCrawlTime must be positive"))
	}
	if c.SitemapMaxURLs <= 0 {
		errs = append(errs, fmt.Errorf("config: Crawl.SitemapMaxURLs must be positive"))
	}
	if c.UserAgent == "" {
		errs = append(errs, fmt.Errorf("config: Crawl.UserAgent is required"))
	}
	if c.AllowPrivateHosts && env == "production" {
		errs = append(errs, fmt.Errorf("config: Crawl.AllowPrivateHosts must not be enabled in production; it makes the crawler an SSRF tool"))
	}
	return errors.Join(errs...)
}

func (e SeedEntry) validate(index int) error {
	var errs []error
	if strings.TrimSpace(e.Name) == "" && strings.TrimSpace(e.Domain) == "" {
		errs = append(errs, fmt.Errorf("config: seed entry %d has neither a name nor a domain", index+1))
	}
	if e.Country != "" && len(e.Country) != 2 {
		errs = append(errs, fmt.Errorf("config: seed entry %d has country %q, which is not an ISO 3166-1 alpha-2 code", index+1, e.Country))
	}
	if e.Confidence != nil && (*e.Confidence < 0 || *e.Confidence > 1) {
		errs = append(errs, fmt.Errorf("config: seed entry %d has confidence %v, which is out of range", index+1, *e.Confidence))
	}
	return errors.Join(errs...)
}

// Seeds returns the seed entries loaded from file. It is a copy so a caller
// cannot mutate the configuration.
func (c Config) Seeds() []SeedEntry {
	out := make([]SeedEntry, len(c.seeds))
	copy(out, c.seeds)
	return out
}

// Redacted returns a copy safe to log or serve over the API. Secrets are
// replaced with a marker rather than dropped, so an operator reading a log can
// tell "no key configured" apart from "key configured but hidden".
func (c Config) Redacted() Config {
	out := c
	out.DB.URL = redactURL(c.DB.URL)
	out.Prov.SearchKey = redactSecret(c.Prov.SearchKey)
	if len(c.Prov.Keys) > 0 {
		out.Prov.Keys = make(map[string]string, len(c.Prov.Keys))
		for name, value := range c.Prov.Keys {
			out.Prov.Keys[name] = redactSecret(value)
		}
	}
	// A seed file is operator data, not configuration, and can be large.
	out.seeds = nil
	return out
}

func redactSecret(s string) string {
	if s == "" {
		return ""
	}
	return "[redacted]"
}

func redactURL(raw string) string {
	if raw == "" {
		return ""
	}
	at := strings.LastIndex(raw, "@")
	if at < 0 {
		return raw
	}
	scheme := ""
	if i := strings.Index(raw, "://"); i >= 0 {
		scheme = raw[:i+3]
	}
	return scheme + "[redacted]" + raw[at:]
}

// requireHTTPS rejects a plaintext provider endpoint. Discovery queries carry
// the operator's API keys, and a key sent over http:// to a hostname is a
// compromised key.
func requireHTTPS(raw, field string) error {
	low := strings.ToLower(raw)
	if strings.HasPrefix(low, "https://") {
		return nil
	}
	// A relative or blank endpoint is resolved against nothing and will fail
	// later; catching it here names the actual mistake.
	return fmt.Errorf("config: %s must be an https:// URL, got %q", field, raw)
}

// isLanguageTag accepts a BCP-47 tag. Case is allowed to vary because BCP-47 is
// case-insensitive by definition and the conventional form is mixed ("pt-BR",
// "zh-Hans"); rejecting "pt-BR" would reject the spelling every real locale list
// uses.
func isLanguageTag(s string) bool {
	if s == "" || len(s) > 35 {
		return false
	}
	parts := strings.Split(s, "-")
	if len(parts) > 8 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 8 {
			return false
		}
		for i := 0; i < len(part); i++ {
			c := part[i]
			if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') {
				return false
			}
		}
	}
	return true
}

func envString(name, def string) string {
	if v, ok := os.LookupEnv(Prefix + name); ok {
		return v
	}
	return def
}

func envStringAny(names []string, def string) string {
	for _, n := range names {
		if v, ok := os.LookupEnv(Prefix + n); ok && v != "" {
			return v
		}
		if v, ok := os.LookupEnv(n); ok && v != "" {
			return v
		}
	}
	return def
}

func envDuration(name string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(Prefix + name); ok && v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
		// A malformed duration is a typo the operator must see, so it is kept
		// verbatim and fails validation rather than silently reverting to the
		// default, which would look like the setting worked.
		return -1
	}
	return def
}

func envInt(name string, def int) int {
	v, ok := os.LookupEnv(Prefix + name)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return -1
	}
	return n
}

func envInt64(name string, def int64) int64 {
	v, ok := os.LookupEnv(Prefix + name)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

func envFloat(name string, def float64) float64 {
	v, ok := os.LookupEnv(Prefix + name)
	if !ok || v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return -1
	}
	return f
}

func envBool(name string, def bool) bool {
	v, ok := os.LookupEnv(Prefix + name)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return !def
	}
	return b
}

// envList parses a comma-separated list. An explicitly empty value yields an
// empty list rather than the default, because "run no optional providers" is a
// legitimate instruction that must not be read as "use the defaults".
func envList(name string, def []string) []string {
	v, ok := os.LookupEnv(Prefix + name)
	if !ok {
		return def
	}
	if strings.TrimSpace(v) == "" {
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

// SeedPathOrDefault returns the seed file path, preferring the Seed block over
// the legacy provider-level variable.
func (c Config) SeedPathOrDefault() string {
	if c.Seed.Path != "" {
		return c.Seed.Path
	}
	return c.Prov.SeedPath
}

// ConfigFileEnv is the variable naming a YAML file.
const ConfigFileEnv = Prefix + "CONFIG_FILE"

// ExampleConfig is a documented starting point, written by hand rather than
// generated so the comments explain why each limit exists.
const ExampleConfig = `# Discovery configuration. Every value here can be overridden by an
# UPVISTA_DISCOVERY_* environment variable, and the environment wins.
env: development

log:
  level: info
  format: text

api:
  addr: ":8081"
  max_body_bytes: 1048576

run:
  # Wall clock, not a page count, is the limit that actually protects an
  # operator's provider bill when a provider starts returning new results.
  max_wall_clock: 30m
  max_candidates: 100000
  max_provider_calls: 20000
  # A query like "logistics software" returns the same twenty vendors on every
  # page. Capping per query is what keeps page one from becoming the candidate
  # set.
  max_candidates_per_query: 50
  max_depth: 2
  concurrency: 4

query:
  max_per_candidate: 12
  max_per_run: 5000
  # A country with fifty cities produces fifty near-identical queries. Three
  # is enough to find a regional market.
  max_cities_per_country: 3
  include_local_language: true
  # Leave empty to use each candidate's own country. Setting it forces the same
  # languages for every run, which is right for a single-market deployment.
  languages: []

ranking:
  accept_threshold: 0.55
  # Candidates in this band are kept and marked for review rather than dropped,
  # so tuning the threshold does not require a re-run to see what is near it.
  review_band: 0.35
  max_candidates_per_domain: 5

providers:
  # Omit to run every configured provider. Listing a name that does not exist
  # is an error, not a silent no-op.
  enabled: []
  disabled: []
  certificate_endpoint: "https://crt.sh/"
  rdap_bootstrap_url: "https://data.iana.org/rdap/dns.json"
  # Set search_endpoint and UPVISTA_DISCOVERY_SEARCH_API_KEY to enable web
  # search. Without a key the provider registers as unready and is skipped.
  search_endpoint: ""
  per_provider_calls: 5000
  request_timeout: 30s
  max_pages_per_provider: 20
  user_agent: "UpvistaDiscovery/1.0 (+https://upvista.example/bot)"
  # Provider API keys are read from UPVISTA_DISCOVERY_PROVIDER_KEY_<NAME> and
  # are never read from this file, because this file gets committed.

crawl:
  # Discovery uses the crawler only to read sitemaps and a few known-good
  # pages, so these are far tighter than the crawler's own defaults.
  enabled: true
  per_host_delay: 2s
  max_pages: 25
  sitemap_max_urls: 500
  # Zero keeps links, not bodies. Discovery has no business holding page bytes.
  raw_retention: 0s
  respect_robots: true
  # Never enable this outside local fixtures. It lets a discovered URL reach
  # 10.x and localhost.
  allow_private_hosts: false

seed:
  path: ""
  default_confidence: 0.9
  max_entries: 50000
`

// WriteExample writes ExampleConfig to a path, refusing to clobber an existing
// file. A CLI that overwrites an operator's tuned configuration is a bug.
func WriteExample(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config: %s already exists", path)
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("config: create %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, []byte(ExampleConfig), 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}
