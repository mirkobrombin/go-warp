package adaptive

import (
	"log/slog"
	"sync"
	"time"

	"github.com/mirkobrombin/go-warp/v1/cache"
	"github.com/prometheus/client_golang/prometheus"
)

// SlidingWindow is a TTLStrategy that detects hot keys using a
// sliding time window and adjusts TTL accordingly.
type SlidingWindow struct {
	Window    time.Duration
	Threshold int
	ColdTTL   time.Duration
	HotTTL    time.Duration

	mu      sync.Mutex
	hits    map[string][]time.Time
	lastTTL map[string]time.Duration
	hotKeys map[string]bool

	adjustCounter prometheus.Counter
	hotGauge      prometheus.Gauge
}

// NewSlidingWindow creates a new SlidingWindow strategy.
// reg may be nil to disable metrics registration.
func NewSlidingWindow(window time.Duration, threshold int, coldTTL, hotTTL time.Duration, reg prometheus.Registerer) *SlidingWindow {
	sw := &SlidingWindow{
		Window:    window,
		Threshold: threshold,
		ColdTTL:   coldTTL,
		HotTTL:    hotTTL,
		hits:      make(map[string][]time.Time),
		lastTTL:   make(map[string]time.Duration),
		hotKeys:   make(map[string]bool),
	}
	if reg != nil {
		sw.adjustCounter = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "warp_adaptive_ttl_adjustments_total",
			Help: "Total number of TTL adjustments by the adaptive strategy",
		})
		sw.hotGauge = prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "warp_adaptive_hot_keys",
			Help: "Number of keys currently considered hot",
		})
		reg.MustRegister(sw.adjustCounter, sw.hotGauge)
	}
	return sw
}

// Record implements cache.TTLStrategy.Record.
func (s *SlidingWindow) Record(key string) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	timestamps := append(s.hits[key], now)
	cutoff := now.Add(-s.Window)
	i := 0
	for _, t := range timestamps {
		if t.After(cutoff) {
			timestamps[i] = t
			i++
		}
	}
	s.hits[key] = timestamps[:i]
}

// TTL implements cache.TTLStrategy.TTL.
func (s *SlidingWindow) TTL(key string) time.Duration {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	timestamps := s.hits[key]
	cutoff := now.Add(-s.Window)
	i := 0
	for _, t := range timestamps {
		if t.After(cutoff) {
			timestamps[i] = t
			i++
		}
	}
	timestamps = timestamps[:i]
	s.hits[key] = timestamps

	ttl := s.ColdTTL
	hot := false
	if len(timestamps) >= s.Threshold {
		ttl = s.HotTTL
		hot = true
	}

	if last, ok := s.lastTTL[key]; !ok || last != ttl {
		slog.Info("adaptive ttl adjusted", "key", key, "ttl", ttl)
		if s.adjustCounter != nil {
			s.adjustCounter.Inc()
		}
		s.lastTTL[key] = ttl
	}

	if hot && !s.hotKeys[key] {
		if s.hotGauge != nil {
			s.hotGauge.Inc()
		}
		s.hotKeys[key] = true
	} else if !hot && s.hotKeys[key] {
		if s.hotGauge != nil {
			s.hotGauge.Dec()
		}
		delete(s.hotKeys, key)
	}

	return ttl
}

var _ cache.TTLStrategy = (*SlidingWindow)(nil)
