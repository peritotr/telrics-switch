// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

func openTestJournal(t *testing.T, dir string) *Journal {
	t.Helper()
	j, err := OpenJournal(dir, DefaultJournalSegment*4)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func recs(from, n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = []byte(fmt.Sprintf(`{"call_id":"c%d","leg":1}`, from+i))
	}
	return out
}

// nextAll takes n records from the journal, failing if they do not come.
func nextAll(t *testing.T, j *Journal, n int) []JournalEntry {
	t.Helper()
	stop := make(chan struct{})
	timer := time.AfterFunc(5*time.Second, func() { close(stop) })
	defer timer.Stop()
	var out []JournalEntry
	for range n {
		e, ok := j.Next(stop)
		if !ok {
			t.Fatalf("got %d records, want %d", len(out), n)
		}
		out = append(out, e)
	}
	return out
}

func segFiles(t *testing.T, dir string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, "cdr-*.log"))
	sort.Strings(m)
	return m
}

func TestJournalAppendReadAck(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir)
	defer j.Close()
	j.SegmentBytes = 100 // a few records a segment
	for i := 0; i < 10; i++ {
		if err := j.Append(recs(i*3, 3)); err != nil {
			t.Fatal(err)
		}
	}
	if j.Depth() != 30 {
		t.Fatalf("depth %d, want 30", j.Depth())
	}
	if n := len(segFiles(t, dir)); n < 5 {
		t.Fatalf("%d segments, want the records spread over several", n)
	}
	got := nextAll(t, j, 30)
	for i, e := range got {
		if want := fmt.Sprintf(`{"call_id":"c%d","leg":1}`, i); string(e.Data) != want {
			t.Fatalf("record %d: %s, want %s", i, e.Data, want)
		}
	}
	// Nothing is removed before NATS has confirmed it.
	if j.Depth() != 30 || j.Bytes() == 0 {
		t.Fatalf("before acks: depth %d, bytes %d", j.Depth(), j.Bytes())
	}
	for _, e := range got {
		j.Ack(e)
	}
	if err := j.Tidy(); err != nil { // close the last one too
		t.Fatal(err)
	}
	if j.Depth() != 0 {
		t.Fatalf("depth %d after acks", j.Depth())
	}
	// Only the empty segment being written is left.
	if files := segFiles(t, dir); len(files) != 1 {
		t.Fatalf("segments left: %v", files)
	}
	if fi, _ := os.Stat(segFiles(t, dir)[0]); fi.Size() != 0 || j.Bytes() != 0 {
		t.Fatalf("left %d bytes on disk, journal counts %d", fi.Size(), j.Bytes())
	}
}

func TestJournalSegmentAge(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir)
	defer j.Close()
	j.SegmentAge = time.Millisecond
	if err := j.Append(recs(0, 1)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := j.Tidy(); err != nil {
		t.Fatal(err)
	}
	e := nextAll(t, j, 1)[0]
	j.Ack(e)
	// A quiet engine does not keep confirmed records for long.
	if files := segFiles(t, dir); len(files) != 1 || j.Bytes() != 0 {
		t.Fatalf("segments %v, bytes %d", files, j.Bytes())
	}
}

func TestJournalReplay(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir)
	j.SegmentBytes = 300
	for i := 0; i < 4; i++ {
		if err := j.Append(recs(i*5, 5)); err != nil {
			t.Fatal(err)
		}
	}
	// Some were confirmed, some handed out and not, some not handed out:
	// then the process dies.
	got := nextAll(t, j, 12)
	for _, e := range got[:7] {
		j.Ack(e)
	}
	j.Close()

	before := mJournalReplayed.Value()
	j = openTestJournal(t, dir)
	defer j.Close()
	// Whole segments are kept until every record in them is confirmed, so
	// some confirmed ones come again; every unconfirmed one does.
	depth := int(j.Depth())
	if depth < 13 || depth > 20 {
		t.Fatalf("depth after reopening %d, want 13..20", depth)
	}
	again := nextAll(t, j, depth)
	seen := map[string]bool{}
	for _, e := range again {
		seen[string(e.Data)] = true
		j.Ack(e)
	}
	for i := 7; i < 20; i++ {
		if !seen[fmt.Sprintf(`{"call_id":"c%d","leg":1}`, i)] {
			t.Fatalf("record c%d was not sent again", i)
		}
	}
	if n := mJournalReplayed.Value() - before; n != uint64(depth) {
		t.Fatalf("replayed %d, want %d", n, depth)
	}
	if j.Depth() != 0 || len(segFiles(t, dir)) != 1 {
		t.Fatalf("depth %d, segments %v", j.Depth(), segFiles(t, dir))
	}
	// New records go after the old ones, in a segment of their own.
	if err := j.Append(recs(100, 1)); err != nil {
		t.Fatal(err)
	}
	if e := nextAll(t, j, 1)[0]; string(e.Data) != `{"call_id":"c100","leg":1}` {
		t.Fatalf("new record %s", e.Data)
	}
}

// A crash in the middle of a write leaves part of a record at the end of
// the segment: it was never confirmed and is dropped, the rest is kept.
func TestJournalTornTail(t *testing.T) {
	for _, tc := range []struct {
		name string
		tail func(last []byte) []byte
	}{
		{"half a header", func([]byte) []byte { return []byte{40, 0, 0} }},
		{"header without its record", func([]byte) []byte { return []byte{40, 0, 0, 0, 1, 2, 3, 4, '{', '"'} }},
		{"no such length", func([]byte) []byte { return []byte{0, 0, 0, 0, 0, 0, 0, 0} }},
		{"bad checksum", func(last []byte) []byte {
			b := append([]byte(nil), last...)
			b[len(b)-1] ^= 0xff
			return b
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			j := openTestJournal(t, dir)
			if err := j.Append(recs(0, 3)); err != nil {
				t.Fatal(err)
			}
			j.Close()
			path := segFiles(t, dir)[0]
			whole, _ := os.ReadFile(path)
			// The last record as framed, to damage a copy of it.
			r := recs(2, 1)[0]
			last := whole[len(whole)-frameHeader-len(r):]
			f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
			f.Write(tc.tail(last))
			f.Close()

			torn := mJournalTorn.Value()
			j = openTestJournal(t, dir)
			defer j.Close()
			if j.Depth() != 3 || mJournalTorn.Value() != torn+1 {
				t.Fatalf("depth %d (want 3), torn counted %d", j.Depth(), mJournalTorn.Value()-torn)
			}
			if fi, _ := os.Stat(path); fi.Size() != int64(len(whole)) {
				t.Fatalf("segment is %d bytes, want it cut back to %d", fi.Size(), len(whole))
			}
			for i, e := range nextAll(t, j, 3) {
				if want := fmt.Sprintf(`{"call_id":"c%d","leg":1}`, i); string(e.Data) != want {
					t.Fatalf("record %d: %s", i, e.Data)
				}
			}
		})
	}
	// A record damaged in the middle takes the rest of its segment with it.
	dir := t.TempDir()
	j := openTestJournal(t, dir)
	j.Append(recs(0, 3))
	j.Close()
	path := segFiles(t, dir)[0]
	b, _ := os.ReadFile(path)
	first := frameHeader + int(binary.LittleEndian.Uint32(b))
	b[first+frameHeader] ^= 0xff
	os.WriteFile(path, b, 0o640)
	j = openTestJournal(t, dir)
	defer j.Close()
	if j.Depth() != 1 {
		t.Fatalf("depth %d, want only the record before the damage", j.Depth())
	}
}

func TestJournalFull(t *testing.T) {
	j, err := OpenJournal(t.TempDir(), DefaultJournalSegment)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	big := [][]byte{make([]byte, 600<<10)}
	if err := j.Append(big); err != nil {
		t.Fatal(err)
	}
	if err := j.Append(big); !errors.Is(err, ErrJournalFull) {
		t.Fatalf("second append: %v, want full", err)
	}
	// Room comes back as NATS confirms.
	e := nextAll(t, j, 1)[0]
	j.Ack(e)
	j.Tidy()
	if err := j.Append(big); err != nil {
		t.Fatalf("after the ack: %v", err)
	}
	if _, err := OpenJournal(t.TempDir(), 1000); err == nil {
		t.Fatal("a journal smaller than a segment was accepted")
	}
}

// fakeStream stands in for JetStream: it keeps one copy per message id, as
// the stream's duplicate window does, and can be made to refuse everything.
type fakeStream struct {
	mu    sync.Mutex
	down  bool
	got   map[string]int // message id -> times offered
	order []string
}

func (f *fakeStream) publish(_ context.Context, data []byte, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return errors.New("nats: no responders")
	}
	if f.got == nil {
		f.got = map[string]int{}
	}
	if f.got[id] == 0 {
		f.order = append(f.order, id)
	}
	f.got[id]++
	return nil
}

func (f *fakeStream) stored() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.order)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !ok(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// NATS is down, the engine queues records and dies; the next engine sends
// them all once NATS is back, and the stream keeps each once.
func TestSinkSurvivesRestartWhileNATSIsDown(t *testing.T) {
	dir := t.TempDir()
	stream := &fakeStream{down: true}
	j := openTestJournal(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	s := newSink(ctx, 4, j, stream.publish)
	for i := range 50 {
		s.Publish(CDR{CallID: fmt.Sprintf("call-%d", i), Leg: 1, SIPCode: 200})
	}
	waitFor(t, "the records on disk", func() bool { return j.Depth() == 50 })
	// The process ends: what NATS never confirmed stays on disk.
	cancel()
	s.Close()

	stream.mu.Lock()
	stream.down = false
	stream.mu.Unlock()
	j = openTestJournal(t, dir)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	s = newSink(ctx, 4, j, stream.publish)
	s.Publish(CDR{CallID: "after", Leg: 1})
	waitFor(t, "every record in the stream", func() bool { return stream.stored() == 51 })
	waitFor(t, "the journal to empty", func() bool { return j.Depth() == 0 })
	for id, n := range stream.got {
		if n != 1 {
			t.Errorf("%s offered %d times", id, n)
		}
	}
	// A record that was confirmed before a crash, and so may come again,
	// is stored once all the same.
	var rec CDR
	if err := json.Unmarshal([]byte(`{"call_id":"call-1","leg":1}`), &rec); err != nil {
		t.Fatal(err)
	}
	s.Publish(rec)
	waitFor(t, "the repeat to be offered", func() bool {
		stream.mu.Lock()
		defer stream.mu.Unlock()
		return stream.got["call-1:1"] == 2
	})
	if stream.stored() != 51 {
		t.Fatalf("%d stored, want 51", stream.stored())
	}
	cancel()
	s.Close()
}

// Records still queued in memory when the engine stops go to disk.
func TestSinkCloseWritesQueue(t *testing.T) {
	dir := t.TempDir()
	stream := &fakeStream{down: true}
	j := openTestJournal(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	s := newSink(ctx, 1, j, stream.publish)
	cancel() // the publishers stop; the writer does not, until Close
	for i := range 1000 {
		s.Publish(CDR{CallID: fmt.Sprintf("q-%d", i), Leg: 1})
	}
	s.Close()
	j = openTestJournal(t, dir)
	defer j.Close()
	if j.Depth() != 1000 {
		t.Fatalf("%d records on disk, want 1000", j.Depth())
	}
}

// A batch the disk does not take is reported and offered again; what was
// committed before it is not touched, and writing goes on in a new segment.
func TestJournalWriteError(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir)
	defer j.Close()
	if err := j.Append(recs(0, 2)); err != nil {
		t.Fatal(err)
	}
	j.w.Close() // the disk goes away under the writer
	if err := j.Append(recs(2, 2)); err == nil {
		t.Fatal("append to a closed file succeeded")
	}
	if j.Depth() != 2 {
		t.Fatalf("depth %d after the failed batch, want 2", j.Depth())
	}
	if err := j.Append(recs(2, 2)); err != nil {
		t.Fatalf("the batch offered again: %v", err)
	}
	for i, e := range nextAll(t, j, 4) {
		if want := fmt.Sprintf(`{"call_id":"c%d","leg":1}`, i); string(e.Data) != want {
			t.Fatalf("record %d: %s", i, e.Data)
		}
		j.Ack(e)
	}
	if len(segFiles(t, dir)) != 1 || j.Depth() != 0 {
		t.Fatalf("segments %v, depth %d", segFiles(t, dir), j.Depth())
	}
}

// A second process on the same directory would send and remove the first
// one's records: it is refused until the first has closed the journal.
func TestJournalOneProcess(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir)
	if _, err := OpenJournal(dir, DefaultJournalSegment*4); err == nil {
		t.Fatal("a second journal on the same directory was opened")
	}
	j.Close()
	j = openTestJournal(t, dir)
	j.Close()
}
