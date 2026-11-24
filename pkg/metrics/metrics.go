// SPDX-License-Identifier: AGPL-3.0-only

// Package metrics keeps the counters the services expose to Prometheus and
// writes them in its text format. It is deliberately small: a counter is
// one atomic add, so it can sit on the call path.
package metrics

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type series interface {
	write(w io.Writer, name, labels string)
}

type family struct {
	help, kind string
	labels     []string // label sets, in registration order
	series     []series
}

var (
	mu       sync.Mutex
	families = map[string]*family{}
)

// register adds one series; labels are "key=value" pairs fixed for its life.
func register(name, help, kind string, s series, labels []string) {
	parts := make([]string, len(labels))
	for i, l := range labels {
		k, v, _ := strings.Cut(l, "=")
		parts[i] = k + "=" + strconv.Quote(v)
	}
	set := strings.Join(parts, ",")
	mu.Lock()
	defer mu.Unlock()
	f := families[name]
	if f == nil {
		f = &family{help: help, kind: kind}
		families[name] = f
	}
	for _, have := range f.labels {
		if have == set {
			panic("metrics: " + name + "{" + set + "} registered twice")
		}
	}
	f.labels, f.series = append(f.labels, set), append(f.series, s)
}

// Counter only goes up.
type Counter struct{ n atomic.Uint64 }

func NewCounter(name, help string, labels ...string) *Counter {
	c := &Counter{}
	register(name, help, "counter", c, labels)
	return c
}

func (c *Counter) Inc()          { c.n.Add(1) }
func (c *Counter) Add(n int)     { c.n.Add(uint64(n)) }
func (c *Counter) Value() uint64 { return c.n.Load() }

func (c *Counter) write(w io.Writer, name, labels string) {
	fmt.Fprintf(w, "%s%s %d\n", name, braces(labels), c.n.Load())
}

type gaugeFunc func() (float64, bool)

func (g gaugeFunc) write(w io.Writer, name, labels string) {
	if v, ok := g(); ok {
		fmt.Fprintf(w, "%s%s %s\n", name, braces(labels), num(v))
	}
}

// NewGauge registers a value read when the metrics are collected. f may be
// slow (a query, say): it runs once per collection, never on the call path.
// When it reports false the value is left out, which reads as "unknown".
func NewGauge(name, help string, f func() (float64, bool), labels ...string) {
	register(name, help, "gauge", gaugeFunc(f), labels)
}

// Histogram counts observations of a duration in seconds into buckets.
type Histogram struct {
	bounds []float64
	counts []atomic.Uint64 // one per bound, plus one for everything above
	sumNs  atomic.Uint64
}

// LatencyBounds suits requests answered in milliseconds.
var LatencyBounds = []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 1}

func NewHistogram(name, help string, bounds []float64, labels ...string) *Histogram {
	h := &Histogram{bounds: bounds, counts: make([]atomic.Uint64, len(bounds)+1)}
	register(name, help, "histogram", h, labels)
	return h
}

func (h *Histogram) Observe(d time.Duration) {
	s := d.Seconds()
	i := sort.SearchFloat64s(h.bounds, s)
	h.counts[i].Add(1)
	h.sumNs.Add(uint64(d))
}

func (h *Histogram) write(w io.Writer, name, labels string) {
	var total uint64
	for i, b := range h.bounds {
		total += h.counts[i].Load()
		fmt.Fprintf(w, "%s_bucket%s %d\n", name, braces(join(labels, `le="`+num(b)+`"`)), total)
	}
	total += h.counts[len(h.bounds)].Load()
	fmt.Fprintf(w, "%s_bucket%s %d\n", name, braces(join(labels, `le="+Inf"`)), total)
	fmt.Fprintf(w, "%s_sum%s %s\n", name, braces(labels), num(float64(h.sumNs.Load())/1e9))
	fmt.Fprintf(w, "%s_count%s %d\n", name, braces(labels), total)
}

func braces(labels string) string {
	if labels == "" {
		return ""
	}
	return "{" + labels + "}"
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "," + b
}

func num(v float64) string {
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// Write prints every registered metric in Prometheus' text format.
func Write(w io.Writer) {
	mu.Lock()
	names := make([]string, 0, len(families))
	for name := range families {
		names = append(names, name)
	}
	sort.Strings(names)
	snapshot := make([]family, len(names))
	for i, name := range names {
		snapshot[i] = *families[name]
	}
	mu.Unlock()
	for i, name := range names {
		f := snapshot[i]
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, f.help, name, f.kind)
		for j, s := range f.series {
			s.write(w, name, f.labels[j])
		}
	}
}

// Handler serves the metrics. They carry no secrets, but they do tell how
// much traffic there is: keep the address private.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		Write(w)
	})
}

// Serve answers GET /metrics on addr until ctx ends, for services that have
// no HTTP listener of their own or whose listener is public.
func Serve(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", Handler())
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
