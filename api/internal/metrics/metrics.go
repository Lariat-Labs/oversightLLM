// Package metrics is a small Prometheus-format registry (counters, histograms and
// callback gauges) for the Oversight service's own health. Fleet activity lives in
// App Insights; these metrics answer "is Oversight itself working?".
package metrics

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

type labels []string // alternating name, value

func (l labels) key() string { return strings.Join(l, "\x00") }

func (l labels) render(extra ...string) string {
	all := append(append([]string{}, l...), extra...)
	if len(all) == 0 {
		return ""
	}
	parts := make([]string, 0, len(all)/2)
	for i := 0; i+1 < len(all); i += 2 {
		v := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(all[i+1])
		parts = append(parts, fmt.Sprintf(`%s="%s"`, all[i], v))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

type family struct {
	name, help, kind string
	mu               sync.Mutex
	counters         map[string]*counterVal
	hists            map[string]*histVal
	buckets          []float64
	gauge            func() []Sample
}

type counterVal struct {
	l labels
	v float64
}

type histVal struct {
	l      labels
	counts []uint64
	sum    float64
	n      uint64
}

// Sample is one gauge reading.
type Sample struct {
	Labels []string
	Value  float64
}

// Registry holds metric families in registration order.
type Registry struct {
	mu   sync.Mutex
	fams []*family
}

// Default is the process-wide registry served at /metrics.
var Default = &Registry{}

func (r *Registry) add(f *family) *family {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fams = append(r.fams, f)
	return f
}

// Counter is a monotonically increasing value per label set.
type Counter struct{ f *family }

// NewCounter registers a counter.
func (r *Registry) NewCounter(name, help string) *Counter {
	return &Counter{r.add(&family{name: name, help: help, kind: "counter", counters: map[string]*counterVal{}})}
}

// Add increments the series for the given name/value label pairs.
func (c *Counter) Add(v float64, kv ...string) {
	l := labels(kv)
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	cv, ok := c.f.counters[l.key()]
	if !ok {
		cv = &counterVal{l: l}
		c.f.counters[l.key()] = cv
	}
	cv.v += v
}

// Inc adds one.
func (c *Counter) Inc(kv ...string) { c.Add(1, kv...) }

// Histogram tracks a distribution in fixed buckets.
type Histogram struct{ f *family }

// DurationBuckets suits HTTP and upstream latencies (seconds).
var DurationBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}

// NewHistogram registers a histogram.
func (r *Registry) NewHistogram(name, help string, buckets []float64) *Histogram {
	return &Histogram{r.add(&family{name: name, help: help, kind: "histogram", hists: map[string]*histVal{}, buckets: buckets})}
}

// Observe records v for the given labels.
func (h *Histogram) Observe(v float64, kv ...string) {
	l := labels(kv)
	h.f.mu.Lock()
	defer h.f.mu.Unlock()
	hv, ok := h.f.hists[l.key()]
	if !ok {
		hv = &histVal{l: l, counts: make([]uint64, len(h.f.buckets))}
		h.f.hists[l.key()] = hv
	}
	for i, b := range h.f.buckets {
		if v <= b {
			hv.counts[i]++
		}
	}
	hv.sum += v
	hv.n++
}

// Since observes the seconds elapsed since start.
func (h *Histogram) Since(start time.Time, kv ...string) {
	h.Observe(time.Since(start).Seconds(), kv...)
}

// NewGauge registers a gauge read from fn at scrape time.
func (r *Registry) NewGauge(name, help string, fn func() []Sample) {
	r.add(&family{name: name, help: help, kind: "gauge", gauge: fn})
}

// Write renders every family in the Prometheus text exposition format (0.0.4).
func (r *Registry) Write(w io.Writer) {
	r.mu.Lock()
	fams := append([]*family(nil), r.fams...)
	r.mu.Unlock()
	for _, f := range fams {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", f.name, f.help, f.name, f.kind)
		switch f.kind {
		case "counter":
			f.mu.Lock()
			for _, cv := range sortedCounters(f.counters) {
				fmt.Fprintf(w, "%s%s %s\n", f.name, cv.l.render(), num(cv.v))
			}
			f.mu.Unlock()
		case "histogram":
			f.mu.Lock()
			for _, hv := range sortedHists(f.hists) {
				for i, b := range f.buckets {
					fmt.Fprintf(w, "%s_bucket%s %d\n", f.name, hv.l.render("le", num(b)), hv.counts[i])
				}
				fmt.Fprintf(w, "%s_bucket%s %d\n", f.name, hv.l.render("le", "+Inf"), hv.n)
				fmt.Fprintf(w, "%s_sum%s %s\n", f.name, hv.l.render(), num(hv.sum))
				fmt.Fprintf(w, "%s_count%s %d\n", f.name, hv.l.render(), hv.n)
			}
			f.mu.Unlock()
		case "gauge":
			for _, s := range f.gauge() {
				fmt.Fprintf(w, "%s%s %s\n", f.name, labels(s.Labels).render(), num(s.Value))
			}
		}
	}
}

func sortedCounters(m map[string]*counterVal) []*counterVal {
	out := make([]*counterVal, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].l.key() < out[j].l.key() })
	return out
}

func sortedHists(m map[string]*histVal) []*histVal {
	out := make([]*histVal, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].l.key() < out[j].l.key() })
	return out
}

func num(v float64) string {
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	return fmt.Sprintf("%g", v)
}

// Outcome maps an error to "ok" or "error" for labels.
func Outcome(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}
