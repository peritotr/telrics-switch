// SPDX-License-Identifier: AGPL-3.0-only

package trace

import (
	"context"
	"errors"
	"log"
	"net"
	"net/netip"
	"slices"
	"sync/atomic"
	"time"

	"softswitch/core/pkg/clickhouse"
	"softswitch/core/pkg/metrics"
)

// Config is what a collector needs.
type Config struct {
	Listen     string // UDP address HEP arrives on
	ClickHouse string // where messages are stored
	KeepDays   int    // 0: DefaultKeepDays
	Queue      int    // messages held while ClickHouse is slow or away; 0: 100000
	Batch      int    // messages per insert; 0: 5000
	Every      time.Duration
	// The SIP core's own addresses, to tell the messages it received from
	// the ones it sent. The address its HEP packets come from always counts.
	Local []netip.Addr
}

// Collector takes SIP messages over HEP and stores them. It never pushes
// back on the sender: what it cannot hold is dropped and counted.
type Collector struct {
	cfg   Config
	ch    *clickhouse.Client
	conn  *net.UDPConn
	queue chan Message
	seq   atomic.Uint64

	received, stored, storeErrors         *metrics.Counter
	droppedBad, droppedFull, droppedStore *metrics.Counter
}

// Listen opens the collector's socket. It registers the collector's
// metrics, so a process calls it once.
func Listen(cfg Config) (*Collector, error) {
	if cfg.KeepDays <= 0 {
		cfg.KeepDays = DefaultKeepDays
	}
	if cfg.Queue <= 0 {
		cfg.Queue = 100000
	}
	if cfg.Batch <= 0 {
		cfg.Batch = 5000
	}
	if cfg.Every <= 0 {
		cfg.Every = time.Second
	}
	ch, err := clickhouse.New(cfg.ClickHouse)
	if err != nil {
		return nil, err
	}
	addr, err := net.ResolveUDPAddr("udp", cfg.Listen)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	// Room for a burst while the reader is busy; the system may grant less.
	conn.SetReadBuffer(16 << 20)
	const dropHelp = "SIP messages the trace collector did not store, by reason."
	c := &Collector{cfg: cfg, ch: ch, conn: conn, queue: make(chan Message, cfg.Queue),
		received:     metrics.NewCounter("softswitch_trace_received_total", "HEP packets that reached the trace collector."),
		stored:       metrics.NewCounter("softswitch_trace_stored_total", "SIP messages the trace collector wrote to ClickHouse."),
		storeErrors:  metrics.NewCounter("softswitch_trace_store_errors_total", "Failed writes of the trace collector to ClickHouse; each is tried again a few times."),
		droppedBad:   metrics.NewCounter("softswitch_trace_dropped_total", dropHelp, "reason=unreadable"),
		droppedFull:  metrics.NewCounter("softswitch_trace_dropped_total", dropHelp, "reason=queue_full"),
		droppedStore: metrics.NewCounter("softswitch_trace_dropped_total", dropHelp, "reason=store_failed"),
	}
	metrics.NewGauge("softswitch_trace_queue_pending", "SIP messages waiting in the trace collector to be written.", func() (float64, bool) {
		return float64(len(c.queue)), true
	})
	return c, nil
}

// Addr is the address the collector listens on.
func (c *Collector) Addr() net.Addr { return c.conn.LocalAddr() }

// Run receives and stores until ctx ends.
func (c *Collector) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		c.conn.Close()
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.write(ctx)
	}()
	defer func() { <-done }() // what is queued is written before leaving
	buf := make([]byte, 65536)
	for {
		n, from, err := c.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return ctx.Err()
			}
			continue
		}
		c.received.Inc()
		m, err := ParseHEP(buf[:n])
		if err != nil {
			c.droppedBad.Inc()
			continue
		}
		// The SIP core sends HEP from its own address, which tells the
		// messages it received from the ones it sent.
		self := from.Addr().Unmap()
		switch {
		case m.Dst.Addr() == self || slices.Contains(c.cfg.Local, m.Dst.Addr()):
			m.Direction = "in"
		case m.Src.Addr() == self || slices.Contains(c.cfg.Local, m.Src.Addr()):
			m.Direction = "out"
		}
		m.Raw = Mask(m.Raw)
		m.Seq = c.seq.Add(1)
		select {
		case c.queue <- m:
		default:
			c.droppedFull.Inc()
		}
	}
}

// write moves messages from the queue to ClickHouse in batches. While
// ClickHouse is away the queue fills and the reader drops what does not
// fit; a batch that keeps failing is given up, so memory stays bounded.
func (c *Collector) write(ctx context.Context) {
	for {
		err := clickhouse.Migrate(ctx, c.ch)
		if err == nil {
			err = SetKeep(ctx, c.ch, c.cfg.KeepDays)
		}
		if err == nil {
			break
		}
		log.Printf("trace: setting up storage: %v (retrying)", err)
		if !sleep(ctx, 5*time.Second) {
			return
		}
	}
	log.Printf("trace: storing signalling for %d days", c.cfg.KeepDays)
	batch := make([]Message, 0, c.cfg.Batch)
	tick := time.NewTicker(c.cfg.Every)
	defer tick.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		for try := 1; ; try++ {
			ictx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			err := Insert(ictx, c.ch, batch)
			cancel()
			if err == nil {
				c.stored.Add(len(batch))
				break
			}
			c.storeErrors.Inc()
			if try == 3 || ctx.Err() != nil {
				log.Printf("trace: %d messages dropped: %v", len(batch), err)
				c.droppedStore.Add(len(batch))
				break
			}
			sleep(ctx, 2*time.Second)
		}
		batch = batch[:0]
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case m := <-c.queue:
			if batch = append(batch, m); len(batch) >= c.cfg.Batch {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
