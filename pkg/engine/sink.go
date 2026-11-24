// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// The stream and subject finished call legs travel on.
const (
	CDRStream  = "CDR"
	CDRSubject = "cdr.v1"
)

// EnsureCDRStream creates the stream if it is missing.
func EnsureCDRStream(ctx context.Context, nc *nats.Conn) (jetstream.JetStream, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name: CDRStream, Subjects: []string{"cdr.>"},
		Storage: jetstream.FileStorage, Retention: jetstream.WorkQueuePolicy,
		Duplicates: 10 * time.Minute,
	})
	return js, err
}

// JournalConfig says where the engine keeps call records until NATS has
// them (ADR-0029). An empty Dir keeps them in memory only, as engines did
// before the journal: a crash then loses those not yet published.
type JournalConfig struct {
	Dir      string
	MaxBytes int64
}

// DefaultJournalMax is the room the journal may take unless told otherwise:
// at 1,000 calls a second about an hour of records.
const DefaultJournalMax = 1 << 30

// JournalFromEnv reads CDR_JOURNAL_DIR and CDR_JOURNAL_MAX_MB.
func JournalFromEnv() (JournalConfig, error) {
	cfg := JournalConfig{Dir: os.Getenv("CDR_JOURNAL_DIR"), MaxBytes: DefaultJournalMax}
	if v := os.Getenv("CDR_JOURNAL_MAX_MB"); v != "" {
		mb, err := strconv.ParseInt(v, 10, 64)
		if err != nil || mb < 1 || mb > 1<<20 {
			return cfg, fmt.Errorf("CDR_JOURNAL_MAX_MB must be a number of megabytes from 1 to %d", 1<<20)
		}
		cfg.MaxBytes = mb << 20
	}
	return cfg, nil
}

// NATSSink hands records to JetStream, retrying until the server has
// stored each one. Publish only puts the record in a queue in memory, so
// the call path never waits for the disk or the network unless that queue
// is full, and then only the end-of-call event does.
//
// With a journal, a background writer takes the records from that queue
// and appends them to the journal, in batches with one sync each; the
// publishers read them back from there and confirm each to the journal
// once NATS has it. Without one, the publishers take them from the queue.
type NATSSink struct {
	nc      *nats.Conn
	queue   chan CDR
	journal *Journal
	ready   chan struct{} // closed once pub may be called
	pub     publishFunc
	work    chan JournalEntry
	stop    chan struct{} // closes the writer, then the journal
	wrote   chan struct{}
	once    sync.Once
}

// publishFunc stores one record under its message id; nil error means the
// server has it.
type publishFunc func(ctx context.Context, data []byte, msgID string) error

// NewNATSSink starts the sink; it does not wait for NATS, which may be
// down while the engine starts (the journal then keeps the records).
func NewNATSSink(ctx context.Context, nc *nats.Conn, workers int, journal *Journal) *NATSSink {
	s := newSink(ctx, workers, journal, nil)
	s.nc = nc
	go s.ensure(ctx)
	return s
}

// newSink starts a sink that stores records with pub; a nil pub is set
// once the stream exists (ensure).
func newSink(ctx context.Context, workers int, journal *Journal, pub publishFunc) *NATSSink {
	s := &NATSSink{queue: make(chan CDR, 200_000), journal: journal, ready: make(chan struct{}), pub: pub,
		stop: make(chan struct{}), wrote: make(chan struct{})}
	if pub != nil {
		close(s.ready)
	}
	if journal == nil {
		close(s.wrote)
		for range workers {
			go s.publishQueue(ctx)
		}
		return s
	}
	s.work = make(chan JournalEntry, 4*workers)
	go s.write()
	go s.read(ctx)
	for range workers {
		go s.publishJournal(ctx)
	}
	return s
}

func (s *NATSSink) Publish(rec CDR) { s.queue <- rec }

// Pending is the number of records waiting in memory: to be written to the
// journal, or without one to be published.
func (s *NATSSink) Pending() int { return len(s.queue) }

// Close writes what is still queued in memory to the journal and closes
// it. Records published after Close stay in memory and are lost with the
// process.
func (s *NATSSink) Close() {
	s.once.Do(func() {
		close(s.stop)
		<-s.wrote
		if s.journal != nil {
			if err := s.journal.Close(); err != nil {
				log.Printf("engine: cdr journal: %v", err)
			}
		}
	})
}

// ensure creates the stream, trying until NATS takes it.
func (s *NATSSink) ensure(ctx context.Context) {
	for wait := 100 * time.Millisecond; ; wait = min(wait*2, 5*time.Second) {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		js, err := EnsureCDRStream(cctx, s.nc)
		cancel()
		if err == nil {
			s.pub = func(ctx context.Context, data []byte, msgID string) error {
				_, err := js.Publish(ctx, CDRSubject, data, jetstream.WithMsgID(msgID))
				return err
			}
			close(s.ready)
			return
		}
		mCDRErrors.Inc()
		log.Printf("engine: cdr stream: %v (retrying)", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// writeBatch is the most records one sync of the journal covers.
const writeBatch = 2048

// write moves records from memory to the journal until Close, then writes
// what is left.
func (s *NATSSink) write() {
	defer close(s.wrote)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	batch := make([][]byte, 0, writeBatch)
	add := func(rec CDR) {
		data, _ := json.Marshal(rec)
		batch = append(batch, data)
	}
	for {
		stopping := false
		select {
		case rec := <-s.queue:
			add(rec)
		case <-tick.C:
			if err := s.journal.Tidy(); err != nil {
				mJournalErrors["write"].Inc()
				log.Printf("engine: %v", err)
			}
			continue
		case <-s.stop:
			stopping = true
		}
	more:
		for len(batch) < writeBatch {
			select {
			case rec := <-s.queue:
				add(rec)
			default:
				break more
			}
		}
		if len(batch) > 0 && !s.flush(batch, stopping) {
			return // stopping and the disk will not take them
		}
		batch = batch[:0]
		if stopping {
			if len(s.queue) > 0 {
				continue
			}
			return
		}
	}
}

// flush appends a batch to the journal, trying again until it is taken.
// While the journal cannot take them the records wait in memory, and the
// queue behind them fills as it did before there was a journal. Stopping,
// it gives up after a few tries.
func (s *NATSSink) flush(batch [][]byte, stopping bool) bool {
	logged := false
	for try, wait := 0, 100*time.Millisecond; ; try, wait = try+1, min(wait*2, 5*time.Second) {
		err := s.journal.Append(batch)
		if err == nil {
			if logged {
				log.Print("engine: cdr journal: writing again")
			}
			return true
		}
		kind := "write"
		if errors.Is(err, ErrJournalFull) {
			kind = "full"
		}
		mJournalErrors[kind].Inc()
		if !logged {
			log.Printf("engine: %v (%d records wait in memory; retrying)", err, len(batch)+len(s.queue))
			logged = true
		}
		if stopping && try >= 3 {
			log.Printf("engine: cdr journal: %d records could not be written before exit", len(batch)+len(s.queue))
			return false
		}
		if stopping {
			time.Sleep(wait)
			continue
		}
		select {
		case <-time.After(wait):
		case <-s.stop:
			stopping = true
		}
	}
}

// read hands the journal's records to the publishers.
func (s *NATSSink) read(ctx context.Context) {
	for {
		e, ok := s.journal.Next(ctx.Done())
		if !ok {
			return
		}
		select {
		case s.work <- e:
		case <-ctx.Done():
			return
		}
	}
}

func (s *NATSSink) publishJournal(ctx context.Context) {
	for {
		select {
		case e := <-s.work:
			var id struct {
				CallID string `json:"call_id"`
				Leg    int    `json:"leg"`
			}
			if err := json.Unmarshal(e.Data, &id); err != nil {
				// Not something this engine wrote; it would never be stored.
				mJournalErrors["read"].Inc()
				log.Printf("engine: cdr journal: unreadable record skipped: %v", err)
				s.journal.Ack(e)
				continue
			}
			if !s.send(ctx, e.Data, id.CallID, id.Leg) {
				return
			}
			s.journal.Ack(e)
		case <-ctx.Done():
			return
		}
	}
}

func (s *NATSSink) publishQueue(ctx context.Context) {
	for {
		select {
		case rec := <-s.queue:
			data, _ := json.Marshal(rec)
			if !s.send(ctx, data, rec.CallID, rec.Leg) {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// send publishes one record until the server has stored it; false when
// ctx ended first.
func (s *NATSSink) send(ctx context.Context, data []byte, callID string, leg int) bool {
	select {
	case <-s.ready:
	case <-ctx.Done():
		return false
	}
	// The message id makes a repeated publish of the same leg a no-op.
	id := callID + ":" + strconv.Itoa(leg)
	for wait := 50 * time.Millisecond; ; wait = min(wait*2, 5*time.Second) {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.pub(pctx, data, id)
		cancel()
		if err == nil {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		mCDRErrors.Inc()
		log.Printf("engine: cdr publish: %v (retrying)", err)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return false
		}
	}
}
