package observe

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCounterAccumulatesByLabelSet(t *testing.T) {
	r := New()
	c := r.Counter("crawl_requests_total", "Crawl requests", "host", "status")
	c.Inc("example.com", "200")
	c.Inc("example.com", "200")
	c.Inc("example.com", "500")
	c.Inc("other.com", "200")

	if got := c.Value("example.com", "200"); got != 2 {
		t.Errorf("example.com/200 = %v, want 2", got)
	}
	if got := c.Value("example.com", "500"); got != 1 {
		t.Errorf("example.com/500 = %v, want 1", got)
	}
	if got := c.Value("other.com", "200"); got != 1 {
		t.Errorf("other.com/200 = %v, want 1", got)
	}
}

func TestGaugeSetsAndAdds(t *testing.T) {
	r := New()
	g := r.Gauge("crawl_frontier_depth", "Pending URLs", "host")
	g.Set(10, "example.com")
	g.Add(-3, "example.com")
	g.Add(5, "other.com")
	if got := g.Value("example.com"); got != 7 {
		t.Errorf("example.com = %v, want 7", got)
	}
	if got := g.Value("other.com"); got != 5 {
		t.Errorf("other.com = %v, want 5", got)
	}
}

func TestHistogramCountsCumulatively(t *testing.T) {
	r := New()
	h := r.Histogram("crawl_duration_seconds", "Fetch latency", []float64{0.1, 1, 10}, "host")
	h.Observe(0.05, "a")
	h.Observe(0.5, "a")
	h.Observe(5, "a")

	out := r.Render()
	// Every label must be present on every sample, including the le label that
	// Prometheus adds to buckets.
	for _, want := range []string{
		`crawl_duration_seconds_bucket{host="a",le="0.1"} 1`,
		`crawl_duration_seconds_bucket{host="a",le="1"} 2`,
		`crawl_duration_seconds_bucket{host="a",le="10"} 3`,
		`crawl_duration_seconds_bucket{host="a",le="+Inf"} 3`,
		`crawl_duration_seconds_sum{host="a"} 5.55`,
		`crawl_duration_seconds_count{host="a"} 3`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q\n%s", want, out)
		}
	}
}

func TestRenderSortsAndLabels(t *testing.T) {
	r := New()
	r.Counter("b_total", "b", "k").Inc("v")
	r.Counter("a_total", "a", "k").Inc("v")
	out := r.Render()
	if strings.Index(out, "a_total") > strings.Index(out, "b_total") {
		t.Errorf("metrics are not rendered in sorted order:\n%s", out)
	}
	if !strings.Contains(out, `b_total{k="v"} 1`) {
		t.Errorf("missing labelled series:\n%s", out)
	}
	if !strings.Contains(out, "# TYPE a_total counter") {
		t.Errorf("missing TYPE line:\n%s", out)
	}
}

func TestUnlabelledMetricHasNoBraces(t *testing.T) {
	r := New()
	r.Counter("runs_total", "runs").Inc()
	if !strings.Contains(r.Render(), "runs_total 1\n") {
		t.Errorf("unlabelled series rendered with a label block:\n%s", r.Render())
	}
}

func TestLabelValuesAreEscaped(t *testing.T) {
	r := New()
	r.Counter("q_total", "q", "url").Inc(`http://x/"y`)
	if !strings.Contains(r.Render(), `q_total{url="http://x/\"y"} 1`) {
		t.Errorf("label value was not escaped:\n%s", r.Render())
	}
}

func TestRegistryIsIdempotent(t *testing.T) {
	r := New()
	if r.Counter("c", "h") != r.Counter("c", "h") {
		t.Error("Counter returned a different instance for the same name")
	}
	if r.Gauge("g", "h") != r.Gauge("g", "h") {
		t.Error("Gauge returned a different instance for the same name")
	}
	if r.Histogram("h", "h", nil) != r.Histogram("h", "h", nil) {
		t.Error("Histogram returned a different instance for the same name")
	}
}

func TestWrongLabelCountPanics(t *testing.T) {
	cases := map[string]func(){
		"too few":  func() { New().Counter("c", "h", "a", "b").Inc("only-one") },
		"too many": func() { New().Counter("c", "h", "a", "b").Inc("x", "y", "z") },
		"spurious": func() { New().Counter("c", "h").Inc("unexpected") },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("a label-count mismatch did not panic")
				}
			}()
			fn()
		})
	}
}

func TestHandlerServesExposition(t *testing.T) {
	r := New()
	r.Counter("c", "h").Inc()
	rec := httptest.NewRecorder()
	r.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "c 1") {
		t.Errorf("body missing metric:\n%s", rec.Body.String())
	}
}

func TestDefaultBucketsAreUsedWhenNoneGiven(t *testing.T) {
	h := New().Histogram("h", "h", nil)
	if len(h.bounds) != len(DefBuckets) {
		t.Fatalf("bounds = %d, want %d", len(h.bounds), len(DefBuckets))
	}
}
