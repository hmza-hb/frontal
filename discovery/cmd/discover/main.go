// Command discover runs lead discovery from the command line.
//
// The CLI is the primary interface: a discovery run is a long, expensive batch
// job whose output an operator wants on disk as well as in the database, and a
// job you can only start over HTTP is a job you cannot put in a cron entry.
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	platformconfig "github.com/hmza-hb/lead-intelligence/platform/config"
	platformdb "github.com/hmza-hb/lead-intelligence/platform/db"
	platformhttpx "github.com/hmza-hb/lead-intelligence/platform/httpx"
	platformlogging "github.com/hmza-hb/lead-intelligence/platform/logging"
	platformobserve "github.com/hmza-hb/lead-intelligence/platform/observe"

	"github.com/hmza-hb/lead-intelligence/discovery/api"
	"github.com/hmza-hb/lead-intelligence/discovery/candidate"
	"github.com/hmza-hb/lead-intelligence/discovery/config"
	"github.com/hmza-hb/lead-intelligence/discovery/persistence"
	"github.com/hmza-hb/lead-intelligence/discovery/providers"
	"github.com/hmza-hb/lead-intelligence/discovery/query"
	"github.com/hmza-hb/lead-intelligence/discovery/service"
	"github.com/jackc/pgx/v5/pgxpool"
)

// version is reported by the API's /version endpoint.
const version = "0.1.0"

const usage = `discover - lead discovery

Usage:
  discover run     [flags]   run discovery over a profile and/or seeds
  discover serve   [flags]   serve the HTTP API
  discover runs    [flags]   list recent runs
  discover report  [flags]   show one run
  discover leads   [flags]   list stored candidates
  discover attempts [flags]  show a run's provider lineage
  discover migrate [flags]   apply this module's database migrations

Run "discover <command> -h" for the flags of a command.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "run":
		err = runCmd(ctx, os.Args[2:])
	case "serve":
		err = serveCmd(ctx, os.Args[2:])
	case "runs":
		err = runsCmd(ctx, os.Args[2:])
	case "report":
		err = reportCmd(ctx, os.Args[2:])
	case "leads":
		err = leadsCmd(ctx, os.Args[2:])
	case "attempts":
		err = attemptsCmd(ctx, os.Args[2:])
	case "migrate":
		err = migrateCmd(ctx, os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "discover: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		// A cancelled run is a normal outcome, not a failure: an operator
		// pressing Ctrl-C should not see a stack of noise.
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "discover: cancelled")
			os.Exit(130)
		}
		fmt.Fprintf(os.Stderr, "discover: %v\n", err)
		os.Exit(1)
	}
}

// newFlagSet builds a flag set that reports errors rather than exiting, so a bad
// flag produces a usage message and an exit code instead of a panic in a
// deferred function.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("discover "+name, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: discover %s [flags]\n\nFlags:\n", name)
		fs.PrintDefaults()
	}
	return fs
}

// loadConfig reads and validates configuration, merging a YAML file if given.
func loadConfig(path string) (config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return cfg, err
	}
	if path != "" {
		if err := cfg.MergeFile(path); err != nil {
			return cfg, err
		}
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// buildProviders constructs the provider set from configuration.
//
// A provider whose credential is missing is still registered, so it reports
// itself unready and is skipped: a run with no search key should find less,
// not fail outright, and the reason belongs in the run report.
func buildProviders(cfg config.Config) ([]providers.Provider, error) {
	// AllowLoopback stays false. It exists so a provider's tests can reach a
	// local fixture server, and a flag that turned it on would let a redirect
	// walk a run into this host's private network.
	fetcher := providers.NewHTTPFetcher(providers.HTTPConfig{
		Timeout:            cfg.Prov.RequestTimeout,
		UserAgent:          cfg.Prov.UserAgent,
		MaxRedirects:       cfg.Prov.MaxRedirects,
		InsecureSkipVerify: cfg.Prov.InsecureSkipVerify,
	})

	var out []providers.Provider
	seed, err := providers.NewSeedProvider(providers.SeedConfig{
		Seeds:             cfg.Seeds(),
		DefaultConfidence: cfg.Seed.DefaultConfidence,
		MaxEntries:        cfg.Seed.MaxEntries,
	})
	if err != nil {
		return nil, err
	}
	out = append(out, seed)

	cert, err := providers.NewCertificateProvider(providers.CertificateConfig{
		Endpoint:    cfg.Prov.CertificateEndpoint,
		Fetcher:     fetcher,
		MaxPages:    cfg.Prov.MaxPagesPerProvider,
		MaxPerQuery: cfg.Run.MaxCandidatesPerQuery,
	})
	if err != nil {
		return nil, err
	}
	out = append(out, cert)

	rdap, err := providers.NewRDAPProvider(providers.RDAPConfig{
		BootstrapURL: cfg.Prov.RDAPBootstrapURL,
		Fetcher:      fetcher,
	})
	if err != nil {
		return nil, err
	}
	out = append(out, rdap)

	search, err := providers.NewSearchProvider(providers.SearchConfig{
		Endpoint:    cfg.Prov.SearchEndpoint,
		APIKey:      cfg.Prov.SearchKey,
		Fetcher:     fetcher,
		MaxPages:    cfg.Prov.MaxPagesPerProvider,
		MaxPerQuery: cfg.Run.MaxCandidatesPerQuery,
	})
	if err != nil {
		return nil, err
	}
	out = append(out, search)

	return out, nil
}

// openStore connects to the database, applying migrations on the way unless the
// caller asked to skip them.
func openStore(ctx context.Context, cfg config.Config, migrate bool) (*persistence.PostgresStore, error) {
	pool, err := openPool(ctx, cfg, "discover")
	if err != nil {
		return nil, err
	}
	if migrate {
		if _, err := platformdb.Migrate(ctx, pool, []platformdb.Source{persistence.MigrationSource()}, nil); err != nil {
			pool.Close()
			return nil, err
		}
	}
	return persistence.NewPostgresStore(pool), nil
}

// openPool dials the database with a bounded startup context, so an unreachable
// database fails the command instead of hanging it.
func openPool(ctx context.Context, cfg config.Config, app string) (*pgxpool.Pool, error) {
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return platformdb.Open(startup, cfg.DB, app)
}

// newService wires the store and providers into a service.
func newService(ctx context.Context, cfg config.Config, log *logger) (*service.Service, *persistence.PostgresStore, error) {
	store, err := openStore(ctx, cfg, true)
	if err != nil {
		return nil, nil, err
	}
	provs, err := buildProviders(cfg)
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	svc, err := service.New(service.Options{
		Config:    cfg,
		Store:     store,
		Providers: provs,
		Log:       log.slog(),
	})
	if err != nil {
		store.Close()
		return nil, nil, err
	}
	return svc, store, nil
}

// logger bundles the configured logger with the level of noise the operator
// asked for. A command that prints a human-readable report should not also
// print a JSON log line for every provider call to the same terminal.
type logger struct {
	inner  *slog.Logger
	report bool
}

// newLogger builds the command logger. quiet drops the log entirely; report
// sends it to stderr so stdout stays parseable.
func newLogger(quiet bool) *logger {
	if quiet {
		return &logger{inner: platformlogging.Discard()}
	}
	cfg, err := config.Load()
	level, format := "info", "text"
	if err == nil {
		level, format = cfg.Log.Level, cfg.Log.Format
	}
	return &logger{inner: platformlogging.New(level, format, os.Stderr), report: true}
}

func (l *logger) slog() *slog.Logger { return l.inner }

func runCmd(ctx context.Context, args []string) error {
	fs := newFlagSet("run")
	cfgPath := fs.String("config", "", "path to a YAML configuration file")
	profileName := fs.String("profile", "", "label for this targeting profile")
	industries := fs.String("industries", "", "comma-separated target industries")
	keywords := fs.String("keywords", "", "comma-separated product or capability keywords")
	technologies := fs.String("technologies", "", "comma-separated technologies whose adopters are the market")
	countries := fs.String("countries", "", "comma-separated ISO 3166-1 alpha-2 target countries")
	competitors := fs.String("competitors", "", "comma-separated companies whose peers are wanted")
	exclude := fs.String("exclude", "", "comma-separated terms that disqualify a result")
	seedFile := fs.String("seeds", "", "CSV or JSON file of operator-supplied companies")
	depth := fs.Int("depth", 0, "expansion rounds (0 uses the configured value)")
	limit := fs.Int("limit", 25, "how many ranked leads to print")
	minScore := fs.Float64("min-score", -1, "only print leads scoring at or above this (negative uses the configured threshold)")
	out := fs.String("out", "", "write the ranked leads to this file as CSV ('-' for stdout)")
	asJSON := fs.Bool("json", false, "print the run report as JSON")
	quiet := fs.Bool("quiet", false, "suppress progress logging")
	if err := fs.Parse(args); err != nil {
		return err
	}

	log := newLogger(*quiet)
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}

	profile := query.Profile{
		Name:         *profileName,
		Industries:   splitList(*industries),
		Keywords:     splitList(*keywords),
		Technologies: splitList(*technologies),
		Countries:    upperAll(splitList(*countries)),
		Competitors:  splitList(*competitors),
		Exclude:      splitList(*exclude),
	}
	if *profileName == "" && len(profile.Industries) == 0 && len(profile.Keywords) == 0 {
		// A profile with no targeting terms generates no questions, and a run
		// that silently returns nothing is worse than one that refuses.
		profile.Name = "ad-hoc"
	}

	var seeds []candidate.Candidate
	if *seedFile != "" {
		seeds, err = loadSeeds(*seedFile, cfg.Seed.MaxEntries)
		if err != nil {
			return err
		}
	}
	if len(seeds) == 0 && len(profile.Industries) == 0 && len(profile.Keywords) == 0 &&
		len(profile.Technologies) == 0 && len(profile.Countries) == 0 {
		return errors.New("nothing to search: pass -industries, -keywords, or -seeds")
	}

	svc, store, err := newService(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer store.Close()

	report, runErr := svc.Run(ctx, service.Request{Profile: profile, Seeds: seeds, MaxDepth: *depth})
	if report == nil {
		return runErr
	}
	if runErr != nil {
		// The report still says how far the run got, which is what an operator
		// needs in order to decide whether to resume it.
		fmt.Fprintf(os.Stderr, "discover: run %s did not complete: %v\n", report.RunID, runErr)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	fmt.Fprintf(os.Stderr, "\nrun %s  %s\n", report.RunID, report.Status)
	fmt.Fprintf(os.Stderr, "  candidates  %d  (accepted %d, rejected %d)\n",
		len(report.Candidates), report.Accepted, report.Rejected)
	fmt.Fprintf(os.Stderr, "  queries     %d asked, %d pending\n", report.QueriesRun, report.QueriesPending)
	fmt.Fprintf(os.Stderr, "  budget      %d of %d calls\n", report.BudgetSpent, cfg.Run.MaxProviderCalls)
	if report.ProviderFailures > 0 {
		fmt.Fprintf(os.Stderr, "  failures    %d provider calls failed\n", report.ProviderFailures)
	}
	for _, name := range sortedKeys(report.ProviderStats) {
		st := report.ProviderStats[name]
		line := fmt.Sprintf("  %-12s %d calls, %d failed", name, st.Calls, st.Failures)
		if !st.Ready {
			line += fmt.Sprintf(" (not ready: %s)", st.ReadyError)
		}
		if st.Truncated > 0 {
			line += fmt.Sprintf(", %d truncated", st.Truncated)
		}
		fmt.Fprintln(os.Stderr, line)
	}

	shown := report.Candidates
	threshold := *minScore
	if threshold < 0 {
		threshold = cfg.Rank.AcceptThreshold
	}
	var kept []service.Scored
	for _, c := range shown {
		if c.Score >= threshold {
			kept = append(kept, c)
		}
	}
	if *limit > 0 && len(kept) > *limit {
		kept = kept[:*limit]
	}
	if len(kept) == 0 {
		fmt.Fprintf(os.Stderr, "\nno lead scored at or above %.2f\n", threshold)
	} else {
		fmt.Fprintf(os.Stderr, "\ntop %d leads (threshold %.2f)\n\n", len(kept), threshold)
		writeTable(os.Stderr, kept)
	}
	if *out != "" && len(kept) > 0 {
		if err := writeCSV(*out, kept); err != nil {
			return fmt.Errorf("write leads: %w", err)
		}
		fmt.Fprintf(os.Stderr, "\nwrote %d leads to %s\n", len(kept), *out)
	}
	if report.QueriesPending > 0 {
		fmt.Fprintf(os.Stderr, "\n%d questions are still pending; a later run will pick them up\n", report.QueriesPending)
	}
	return runErr
}

// writeTable prints ranked leads in aligned columns.
func writeTable(w io.Writer, leads []service.Scored) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RANK\tSCORE\tVERDICT\tCOMPANY\tDOMAIN\tCOUNTRY\tINDUSTRY\tSOURCES")
	for _, l := range leads {
		srcs := 0
		seen := map[candidate.Source]bool{}
		for _, e := range l.Candidate.Evidence {
			if !seen[e.Source] {
				seen[e.Source] = true
				srcs++
			}
		}
		fmt.Fprintf(tw, "%d\t%.3f\t%s\t%s\t%s\t%s\t%s\t%d\n",
			l.Rank, l.Score, l.Verdict,
			truncate(l.Candidate.Name, 34), l.Candidate.Domain,
			l.Candidate.Country, truncate(l.Candidate.Industry, 26), srcs)
	}
	_ = tw.Flush()
}

// writeCSV writes ranked leads for a spreadsheet.
func writeCSV(path string, leads []service.Scored) error {
	var (
		w   io.WriteCloser
		err error
	)
	if path == "-" {
		w, err = os.Stdout, nil
	} else {
		w, err = os.Create(path)
	}
	if err != nil {
		return err
	}
	if w != nil {
		defer w.Close()
	}
	cw := csv.NewWriter(w)
	// The header names the evidence columns explicitly: a lead list handed to a
	// salesperson has to carry its own justification, because that is what gets
	// challenged in a sales call.
	if err := cw.Write([]string{
		"rank", "score", "verdict", "name", "domain", "url", "country",
		"industry", "employee_hint", "sources", "first_seen", "last_seen",
		"explanation", "evidence_sources", "evidence_urls",
	}); err != nil {
		return err
	}
	for _, l := range leads {
		var srcs, urls []string
		seen := map[candidate.Source]bool{}
		for _, e := range l.Candidate.Evidence {
			if !seen[e.Source] {
				seen[e.Source] = true
				srcs = append(srcs, string(e.Source))
			}
			if e.URL != "" {
				urls = append(urls, e.URL)
			}
		}
		row := []string{
			strconv.Itoa(l.Rank), strconv.FormatFloat(l.Score, 'f', 4, 64), l.Verdict,
			l.Candidate.Name, l.Candidate.Domain, l.Candidate.URL, l.Candidate.Country,
			l.Candidate.Industry, l.Candidate.EmployeeHint,
			strconv.Itoa(len(srcs)),
			l.Candidate.FirstSeen.Format(time.RFC3339), l.Candidate.LastSeen.Format(time.RFC3339),
			l.Explain, strings.Join(srcs, " "), strings.Join(urls, " "),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func serveCmd(ctx context.Context, args []string) error {
	fs := newFlagSet("serve")
	cfgPath := fs.String("config", "", "path to a YAML configuration file")
	addr := fs.String("addr", "", "listen address (overrides the configuration)")
	quiet := fs.Bool("quiet", false, "suppress progress logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := newLogger(*quiet)
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	if *addr != "" {
		cfg.API.Addr = *addr
	}
	log.inner.Info("starting discover", "version", version, "addr", cfg.API.Addr)

	pool, err := openPool(ctx, cfg, "discover")
	if err != nil {
		return err
	}
	defer pool.Close()
	if _, err := platformdb.Migrate(ctx, pool, []platformdb.Source{persistence.MigrationSource()}, log.slog()); err != nil {
		return err
	}
	store := persistence.NewPostgresStore(pool)

	provs, err := buildProviders(cfg)
	if err != nil {
		return err
	}
	svc, err := service.New(service.Options{Config: cfg, Store: store, Providers: provs, Log: log.slog()})
	if err != nil {
		return err
	}
	a, err := api.New(api.Options{Service: svc, Log: log.slog()})
	if err != nil {
		return err
	}

	registry := platformobserve.New()
	srv := platformhttpx.NewServer(
		platformconfig.HTTPConfig{
			Addr:              cfg.API.Addr,
			ReadTimeout:       cfg.API.ReadTimeout,
			ReadHeaderTimeout: cfg.API.ReadHeaderTimeout,
			WriteTimeout:      cfg.API.WriteTimeout,
			IdleTimeout:       cfg.API.IdleTimeout,
			ShutdownGrace:     cfg.API.ShutdownGrace,
			MaxBodyBytes:      cfg.API.MaxBodyBytes,
		},
		log.slog(),
		platformhttpx.WithVersion(version),
		platformhttpx.WithMetrics(registry),
		platformhttpx.WithReadiness(func(ctx context.Context) error { return pool.Ping(ctx) }),
	)
	// The API handler already carries the request id, panic and logging
	// middleware, so the server mounts it as-is rather than chaining a second
	// layer that would log every request twice.
	mux := http.NewServeMux()
	mux.Handle("/", a.Handler())
	return srv.Run(ctx, mux)
}

// runsCmd lists recent runs. It takes no logger because it only reads.
func runsCmd(ctx context.Context, args []string) error {
	fs := newFlagSet("runs")
	cfgPath := fs.String("config", "", "path to a YAML configuration file")
	limit := fs.Int("limit", 20, "how many runs to list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	store, err := openStore(ctx, cfg, false)
	if err != nil {
		return err
	}
	defer store.Close()
	runs, err := store.RecentRuns(ctx, *limit)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Fprintln(os.Stderr, "no runs yet")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN\tSTATUS\tPROFILE\tSEEDS\tFOUND\tACCEPTED\tREJECTED\tCALLS\tSTARTED")
	for _, r := range runs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%s\n",
			r.ID, r.Status, r.ProfileName, r.SeedsTotal, r.Candidates,
			r.Accepted, r.Rejected, r.ProviderCalls,
			r.StartedAt.Format(time.RFC3339))
	}
	return tw.Flush()
}

func reportCmd(ctx context.Context, args []string) error {
	fs := newFlagSet("report")
	cfgPath := fs.String("config", "", "path to a YAML configuration file")
	id := fs.String("id", "", "run id")
	asJSON := fs.Bool("json", false, "print JSON instead of a table")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return errors.New("-id is required")
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	store, err := openStore(ctx, cfg, false)
	if err != nil {
		return err
	}
	defer store.Close()

	run, err := store.Run(ctx, *id)
	if err != nil {
		return err
	}
	cands, err := store.Candidates(ctx, persistence.CandidateFilter{RunID: *id, Limit: persistence.MaxQueryLimit})
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"run": run, "candidates": cands})
	}
	fmt.Fprintf(os.Stderr, "run %s  %s\n", run.ID, run.Status)
	fmt.Fprintf(os.Stderr, "  profile   %s\n", run.ProfileName)
	fmt.Fprintf(os.Stderr, "  seeds     %d\n", run.SeedsTotal)
	fmt.Fprintf(os.Stderr, "  found     %d (accepted %d, rejected %d)\n", run.Candidates, run.Accepted, run.Rejected)
	fmt.Fprintf(os.Stderr, "  queries   %d asked, %d pending\n", run.QueriesRun, run.QueriesPending)
	fmt.Fprintf(os.Stderr, "  budget    %d of %d calls\n", run.BudgetSpent, run.BudgetLimit)
	fmt.Fprintf(os.Stderr, "  started   %s\n", run.StartedAt.Format(time.RFC3339))
	if !run.FinishedAt.IsZero() {
		fmt.Fprintf(os.Stderr, "  finished  %s\n", run.FinishedAt.Format(time.RFC3339))
	}
	if run.Error != "" {
		fmt.Fprintf(os.Stderr, "  error     %s\n", run.Error)
	}
	if len(cands) == 0 {
		return nil
	}
	fmt.Fprintf(os.Stderr, "\n%d candidates\n\n", len(cands))
	leads := make([]service.Scored, 0, len(cands))
	for i, c := range cands {
		sc := service.Scored{
			ID: c.ID, Candidate: c.Candidate, Score: c.Score, Verdict: c.Verdict,
			Explain: c.RankExplain, Factors: c.RankFactors, Rank: i + 1,
		}
		leads = append(leads, sc)
	}
	writeTable(os.Stdout, leads)
	return nil
}

func leadsCmd(ctx context.Context, args []string) error {
	fs := newFlagSet("leads")
	cfgPath := fs.String("config", "", "path to a YAML configuration file")
	status := fs.String("status", "", "filter by lifecycle status")
	verdict := fs.String("verdict", "", "filter by ranking verdict")
	runID := fs.String("run", "", "restrict to candidates a run touched")
	limit := fs.Int("limit", 50, "how many leads to list")
	offset := fs.Int("offset", 0, "how many leads to skip")
	out := fs.String("out", "", "write the leads to this file as CSV ('-' for stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	store, err := openStore(ctx, cfg, false)
	if err != nil {
		return err
	}
	defer store.Close()

	cands, err := store.Candidates(ctx, persistence.CandidateFilter{
		Status: *status, Verdict: *verdict, RunID: *runID,
		Limit: *limit, Offset: *offset,
	})
	if err != nil {
		return err
	}
	if len(cands) == 0 {
		fmt.Fprintln(os.Stderr, "no candidates matched")
		return nil
	}
	if *out != "" {
		leads := make([]service.Scored, 0, len(cands))
		for i, c := range cands {
			leads = append(leads, service.Scored{
				ID: c.ID, Candidate: c.Candidate, Score: c.Score, Verdict: c.Verdict,
				Explain: c.RankExplain, Factors: c.RankFactors, Rank: i + 1,
			})
		}
		return writeCSV(*out, leads)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSCORE\tVERDICT\tCOMPANY\tDOMAIN\tCOUNTRY\tINDUSTRY\tLAST SEEN")
	for i, c := range cands {
		score := "-"
		if c.HasScore {
			score = strconv.FormatFloat(c.Score, 'f', 3, 64)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			c.ID, score, c.Verdict, truncate(c.Candidate.Name, 34), c.Candidate.Domain,
			c.Candidate.Country, truncate(c.Candidate.Industry, 26),
			c.Candidate.LastSeen.Format("2006-01-02"))
		_ = i
	}
	return tw.Flush()
}

func attemptsCmd(ctx context.Context, args []string) error {
	fs := newFlagSet("attempts")
	cfgPath := fs.String("config", "", "path to a YAML configuration file")
	id := fs.String("id", "", "run id")
	pendingOnly := fs.Bool("pending", false, "only show questions with work left")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return errors.New("-id is required")
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	store, err := openStore(ctx, cfg, false)
	if err != nil {
		return err
	}
	defer store.Close()

	var attempts []lineageAttempt
	if *pendingOnly {
		raw, err := store.PendingAttempts(ctx, *id)
		if err != nil {
			return err
		}
		for _, a := range raw {
			attempts = append(attempts, lineageAttempt{
				Provider: a.Provider, Query: a.Query, Outcome: string(a.Outcome),
				Candidates: a.Candidates, Cursor: a.Cursor, Error: a.Error, At: a.At,
			})
		}
	} else {
		raw, err := store.AttemptsFor(ctx, *id)
		if err != nil {
			return err
		}
		for _, a := range raw {
			attempts = append(attempts, lineageAttempt{
				Provider: a.Provider, Query: a.Query, Outcome: string(a.Outcome),
				Candidates: a.Candidates, Cursor: a.Cursor, Error: a.Error, At: a.At,
			})
		}
	}
	if len(attempts) == 0 {
		fmt.Fprintln(os.Stderr, "no provider attempts recorded")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROVIDER\tOUTCOME\tFOUND\tQUERY\tWHEN")
	for _, a := range attempts {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n",
			a.Provider, a.Outcome, a.Candidates, truncate(a.Query, 60),
			a.At.Format("2006-01-02 15:04:05"))
	}
	return tw.Flush()
}

// lineageAttempt is one row of the attempts table.
type lineageAttempt struct {
	Provider   string
	Query      string
	Outcome    string
	Candidates int
	Cursor     string
	Error      string
	At         time.Time
}

func migrateCmd(ctx context.Context, args []string) error {
	fs := newFlagSet("migrate")
	cfgPath := fs.String("config", "", "path to a YAML configuration file")
	quiet := fs.Bool("quiet", false, "suppress progress logging")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log := newLogger(*quiet)
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		return err
	}
	pool, err := openPool(ctx, cfg, "discover-migrate")
	if err != nil {
		return err
	}
	defer pool.Close()
	res, err := platformdb.Migrate(ctx, pool, []platformdb.Source{persistence.MigrationSource()}, log.slog())
	if err != nil {
		return err
	}
	fmt.Printf("applied %d migration(s), skipped %d already current\n", len(res.Applied), len(res.Skipped))
	return nil
}

// loadSeeds reads an operator's seed file into run candidates.
//
// Parsing is delegated to the config package so that the CLI, the service and
// the seed provider all agree on what a file means. Building the candidate here
// rather than passing the file through means the run reports seeds as
// discovered candidates, and the operator sees the seed list in the output next
// to everything the run found.
func loadSeeds(path string, maxEntries int) ([]candidate.Candidate, error) {
	if maxEntries <= 0 {
		// LoadSeeds refuses a non-positive maximum rather than reading an
		// unbounded file, and a CLI flag has no configured default to inherit.
		maxEntries = defaultMaxSeeds
	}
	entries, err := config.LoadSeeds(path, maxEntries)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s contains no seeds", path)
	}
	now := time.Now().UTC()
	out := make([]candidate.Candidate, 0, len(entries))
	for _, e := range entries {
		conf := cfgDefaultConfidence
		if e.Confidence != nil {
			conf = *e.Confidence
		}
		c := candidate.Candidate{
			Name:         e.Name,
			Domain:       e.Domain,
			Country:      e.Country,
			Industry:     e.Industry,
			EmployeeHint: e.EmployeeHint,
			Description:  e.Notes,
			Confidence:   conf,
			FirstSeen:    now,
			LastSeen:     now,
			Status:       candidate.StatusNew,
		}
		if c.Domain != "" {
			c.URL = "https://" + c.Domain + "/"
		}
		c.Evidence = []candidate.Evidence{{
			Source:     candidate.SourceSeed,
			Method:     candidate.MethodSeedImport,
			URL:        "file://" + filepath.ToSlash(path),
			Snippet:    strings.TrimSpace(e.Name + " " + e.Notes),
			ObservedAt: now,
		}}
		out = append(out, c)
	}
	return out, nil
}

// cfgDefaultConfidence is the confidence given to a seed file that states none.
// A seed is a human assertion, so it starts confident but not certain.
const cfgDefaultConfidence = 0.5

// defaultMaxSeeds bounds a -seeds file when the configuration states no limit.
const defaultMaxSeeds = 1000

// splitList parses a comma-separated flag value, dropping blanks.
func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// upperAll upper-cases a list, for country codes.
func upperAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, strings.ToUpper(v))
	}
	return out
}

// truncate shortens a string for a fixed-width column.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// sortedKeys gives a deterministic iteration order, so output does not change
// between runs of the same command.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
