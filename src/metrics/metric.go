package metrics

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Metric type names as they appear on the "# TYPE" line of the Prometheus
// text exposition format.
const (
	TypeCounter   = "counter"
	TypeGauge     = "gauge"
	TypeHistogram = "histogram"
)

// Label is a single Prometheus label name/value pair. Label names are snake
// case and lowercase; values must stay low cardinality (never a user id, a
// request id, or a raw client IP).
type Label struct {
	Name  string
	Value string
}

// Bucket is one cumulative histogram bucket. Count is the number of
// observations less than or equal to UpperBound.
type Bucket struct {
	UpperBound float64
	Count      uint64
}

// Sample is a single collected time series. Value carries the current value
// for counters and gauges; Buckets, Sum, and Count carry the histogram
// state and are empty/zero for the other types.
type Sample struct {
	Name    string
	Help    string
	Type    string
	Labels  []Label
	Value   float64
	Buckets []Bucket
	Sum     float64
	Count   uint64
}

// instance is the storage behind one label set of a metric family.
type instance interface {
	snapshot() Sample
}

// Counter is a cumulative value that only ever increases. Counter names end
// in _total per the PART 21 naming convention.
type Counter struct {
	bits atomic.Uint64
}

// Inc adds one to the counter.
func (c *Counter) Inc() {
	c.Add(1)
}

// Add increases the counter by delta. A negative delta is ignored: counters
// never decrease, and a metrics bug must not take the server down.
func (c *Counter) Add(delta float64) {
	if delta < 0 || math.IsNaN(delta) {
		return
	}

	addFloat(&c.bits, delta)
}

// Value returns the current counter value.
func (c *Counter) Value() float64 {
	return math.Float64frombits(c.bits.Load())
}

func (c *Counter) snapshot() Sample {
	return Sample{Type: TypeCounter, Value: c.Value()}
}

// Gauge is a value that can go up or down, such as an in-flight request
// count or a pool size.
type Gauge struct {
	bits atomic.Uint64
}

// Set replaces the gauge value.
func (g *Gauge) Set(v float64) {
	g.bits.Store(math.Float64bits(v))
}

// SetToCurrentTime sets the gauge to the current Unix timestamp in seconds.
func (g *Gauge) SetToCurrentTime() {
	g.Set(float64(time.Now().Unix()))
}

// Inc adds one to the gauge.
func (g *Gauge) Inc() {
	g.Add(1)
}

// Dec subtracts one from the gauge.
func (g *Gauge) Dec() {
	g.Add(-1)
}

// Add increases the gauge by delta, which may be negative.
func (g *Gauge) Add(delta float64) {
	if math.IsNaN(delta) {
		return
	}

	addFloat(&g.bits, delta)
}

// Sub decreases the gauge by delta.
func (g *Gauge) Sub(delta float64) {
	g.Add(-delta)
}

// Value returns the current gauge value.
func (g *Gauge) Value() float64 {
	return math.Float64frombits(g.bits.Load())
}

func (g *Gauge) snapshot() Sample {
	return Sample{Type: TypeGauge, Value: g.Value()}
}

// Histogram counts observations into a fixed set of cumulative buckets and
// tracks their running sum and count.
type Histogram struct {
	bounds []float64

	mu     sync.Mutex
	counts []uint64
	sum    float64
	count  uint64
}

// newHistogram returns a histogram over a sorted, de-duplicated copy of
// bounds. Infinite and NaN bounds are dropped: the +Inf bucket is implicit.
func newHistogram(bounds []float64) *Histogram {
	cleaned := make([]float64, 0, len(bounds))
	for _, b := range bounds {
		if math.IsNaN(b) || math.IsInf(b, 0) {
			continue
		}
		cleaned = append(cleaned, b)
	}

	sort.Float64s(cleaned)

	deduped := cleaned[:0]
	for i, b := range cleaned {
		if i > 0 && b == cleaned[i-1] {
			continue
		}
		deduped = append(deduped, b)
	}

	return &Histogram{bounds: deduped, counts: make([]uint64, len(deduped))}
}

// Observe records one observation. NaN observations are ignored because they
// would poison the running sum for every consumer of the series.
func (h *Histogram) Observe(v float64) {
	if math.IsNaN(v) {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.count++
	h.sum += v

	for i, b := range h.bounds {
		if v <= b {
			h.counts[i]++
		}
	}
}

// ObserveDuration records d as a number of seconds, the base unit PART 21
// requires for every duration metric.
func (h *Histogram) ObserveDuration(d time.Duration) {
	h.Observe(d.Seconds())
}

// Count returns the total number of observations.
func (h *Histogram) Count() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.count
}

// Sum returns the sum of all observed values.
func (h *Histogram) Sum() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.sum
}

// Buckets returns the cumulative bucket counts, excluding the implicit +Inf
// bucket, which always equals Count.
func (h *Histogram) Buckets() []Bucket {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.bucketsLocked()
}

func (h *Histogram) bucketsLocked() []Bucket {
	out := make([]Bucket, len(h.bounds))
	for i, b := range h.bounds {
		out[i] = Bucket{UpperBound: b, Count: h.counts[i]}
	}

	return out
}

func (h *Histogram) snapshot() Sample {
	h.mu.Lock()
	defer h.mu.Unlock()

	return Sample{
		Type:    TypeHistogram,
		Buckets: h.bucketsLocked(),
		Sum:     h.sum,
		Count:   h.count,
	}
}

// addFloat atomically adds delta to a float64 stored as raw bits.
func addFloat(u *atomic.Uint64, delta float64) {
	for {
		old := u.Load()
		updated := math.Float64bits(math.Float64frombits(old) + delta)
		if u.CompareAndSwap(old, updated) {
			return
		}
	}
}
