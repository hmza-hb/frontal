// Package observe is a dependency-free Prometheus-compatible metrics
// registry. Pulling in client_golang for three metric types is not a trade
// this platform makes, and hand-rolling keeps the exposition format stable.
package observe

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// DefBuckets are latency buckets in seconds, chosen for network calls.
var DefBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// Registry holds a set of metrics and renders them for scraping.
type Registry struct {
	mu       sync.RWMutex
	counters map[string]*Counter
	gauges   map[string]*Gauge
	histos   map[string]*Histogram
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{
		counters: make(map[string]*Counter),
		gauges:   make(map[string]*Gauge),
		histos:   make(map[string]*Histogram),
	}
}

// Counter is a monotonically increasing metric partitioned by label values
// supplied positionally in the order given at construction.
type Counter struct {
	name, help string
	labels     []string
	mu         sync.RWMutex
	series     map[string]float64
	order      []string
}

// Gauge is a metric that can go up and down.
type Gauge struct {
	name, help string
	labels     []string
	mu         sync.RWMutex
	series     map[string]float64
	order      []string
}

// Histogram accumulates observations into fixed buckets.
type Histogram struct {
	name, help string
	labels     []string
	bounds     []float64
	mu         sync.RWMutex
	series     map[string]*histogramSeries
	order      []string
}

type histogramSeries struct {
	counts []uint64
	sum    float64
	count  uint64
}

// Counter registers (or returns) a counter. Label names are positional.
func (r *Registry) Counter(name, help string, labels ...string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.counters[name]; ok {
		return c
	}
	c := &Counter{name: name, help: help, labels: labels, series: map[string]float64{}}
	r.counters[name] = c
	return c
}

// Gauge registers (or returns) a gauge.
func (r *Registry) Gauge(name, help string, labels ...string) *Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.gauges[name]; ok {
		return g
	}
	g := &Gauge{name: name, help: help, labels: labels, series: map[string]float64{}}
	r.gauges[name] = g
	return g
}

// Histogram registers (or returns) a histogram. Passing no bounds selects
// DefBuckets.
func (r *Registry) Histogram(name, help string, bounds []float64, labels ...string) *Histogram {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.histos[name]; ok {
		return h
	}
	if len(bounds) == 0 {
		bounds = DefBuckets
	}
	sorted := append([]float64(nil), bounds...)
	sort.Float64s(sorted)
	h := &Histogram{
		name: name, help: help, labels: labels, bounds: sorted,
		series: map[string]*histogramSeries{},
	}
	r.histos[name] = h
	return h
}

// Add increments the series identified by the positional label values.
func (c *Counter) Add(delta float64, labelValues ...string) {
	key := seriesKey(c.name, "", c.labels, labelValues)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.series[key]; !ok {
		c.order = append(c.order, key)
	}
	c.series[key] += delta
}

// Inc adds one.
func (c *Counter) Inc(labelValues ...string) { c.Add(1, labelValues...) }

// Value returns the current total for a series. Test and diagnostic use.
func (c *Counter) Value(labelValues ...string) float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.series[seriesKey(c.name, "", c.labels, labelValues)]
}

// Set assigns a gauge value.
func (g *Gauge) Set(v float64, labelValues ...string) {
	key := seriesKey(g.name, "", g.labels, labelValues)
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.series[key]; !ok {
		g.order = append(g.order, key)
	}
	g.series[key] = v
}

// Add adjusts a gauge by delta.
func (g *Gauge) Add(delta float64, labelValues ...string) {
	key := seriesKey(g.name, "", g.labels, labelValues)
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.series[key]; !ok {
		g.order = append(g.order, key)
	}
	g.series[key] += delta
}

// Value returns the gauge for a series. Test and diagnostic use.
func (g *Gauge) Value(labelValues ...string) float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.series[seriesKey(g.name, "", g.labels, labelValues)]
}

// Observe records one sample.
func (h *Histogram) Observe(v float64, labelValues ...string) {
	key := seriesKey(h.name, "", h.labels, labelValues)
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.series[key]
	if !ok {
		s = &histogramSeries{counts: make([]uint64, len(h.bounds))}
		h.series[key] = s
		h.order = append(h.order, key)
	}
	for i, b := range h.bounds {
		if v <= b {
			s.counts[i]++
		}
	}
	s.sum += v
	s.count++
}

// seriesKey renders the label values into a series key. A label-count mismatch
// is a programming error rather than a runtime condition, so it panics instead
// of silently merging distinct series together.
func seriesKey(metric, _ string, labels, values []string) string {
	if len(values) != len(labels) {
		panic(fmt.Sprintf("metric %s: got %d label values, want %d", metric, len(values), len(labels)))
	}
	if len(labels) == 0 {
		return ""
	}
	var b strings.Builder
	for i, v := range values {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(labels[i])
		b.WriteString(`="`)
		b.WriteString(escapeLabel(v))
		b.WriteByte('"')
	}
	return b.String()
}

// escapeLabel escapes a Prometheus label value.
func escapeLabel(v string) string {
	r := strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
	return r.Replace(v)
}

func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// Handler renders the registry in Prometheus text exposition format.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(r.Render()))
	})
}

// Render returns the exposition text. Exported so tests and the CLI can print
// a snapshot without an HTTP round trip.
func (r *Registry) Render() string {
	r.mu.RLock()
	counterNames := sortedKeys(r.counters)
	gaugeNames := sortedKeys(r.gauges)
	histNames := sortedKeys(r.histos)
	r.mu.RUnlock()

	var b strings.Builder
	for _, name := range counterNames {
		r.mu.RLock()
		m := r.counters[name]
		r.mu.RUnlock()
		writeHeader(&b, m.name, m.help, "counter")
		m.mu.RLock()
		for _, key := range m.order {
			writeSeries(&b, m.name, m.labels, key, formatFloat(m.series[key]))
		}
		m.mu.RUnlock()
	}
	for _, name := range gaugeNames {
		r.mu.RLock()
		m := r.gauges[name]
		r.mu.RUnlock()
		writeHeader(&b, m.name, m.help, "gauge")
		m.mu.RLock()
		for _, key := range m.order {
			writeSeries(&b, m.name, m.labels, key, formatFloat(m.series[key]))
		}
		m.mu.RUnlock()
	}
	for _, name := range histNames {
		r.mu.RLock()
		m := r.histos[name]
		r.mu.RUnlock()
		writeHeader(&b, m.name, m.help, "histogram")
		m.mu.RLock()
		for _, key := range m.order {
			s := m.series[key]
			// counts[i] is already cumulative: Observe increments every
			// bucket whose upper bound is >= the observation.
			for i, bound := range m.bounds {
				b.WriteString(m.name + "_bucket" + bucketSuffix(m.labels, key, formatFloat(bound)) +
					" " + strconv.FormatUint(s.counts[i], 10) + "\n")
			}
			b.WriteString(m.name + "_bucket" + bucketSuffix(m.labels, key, "+Inf") + " " +
				strconv.FormatUint(s.count, 10) + "\n")
			b.WriteString(m.name + "_sum" + labelSuffix(m.labels, key) + " " + formatFloat(s.sum) + "\n")
			b.WriteString(m.name + "_count" + labelSuffix(m.labels, key) + " " + strconv.FormatUint(s.count, 10) + "\n")
		}
		m.mu.RUnlock()
	}
	return b.String()
}

func writeHeader(b *strings.Builder, name, help, typ string) {
	if help != "" {
		b.WriteString("# HELP " + name + " " + help + "\n")
	}
	b.WriteString("# TYPE " + name + " " + typ + "\n")
}

func writeSeries(b *strings.Builder, name string, labels []string, key, value string) {
	b.WriteString(name + labelSuffix(labels, key) + " " + value + "\n")
}

// bucketSuffix renders the label block for a histogram bucket, appending the
// le bound to the series' own labels. Prometheus requires every label to be
// present on every sample, so this cannot be composed from labelSuffix alone.
func bucketSuffix(labels []string, key, bound string) string {
	if len(labels) == 0 {
		return `{le="` + bound + `"}`
	}
	return "{" + key + `,le="` + bound + `"}`
}

// labelSuffix renders `{a="1",b="2"}`, or "" when the metric has no labels.
func labelSuffix(labels []string, key string) string {
	if len(labels) == 0 {
		return ""
	}
	if key == "" {
		return "{}"
	}
	return "{" + key + "}"
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
