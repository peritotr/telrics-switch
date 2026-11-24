// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// A Journal keeps call records on disk from the moment the engine has them
// until NATS has confirmed each one (ADR-0029), so that a crash, a restart
// or a long NATS outage loses none. It is a queue of segment files written
// at one end and read at the other; a segment is removed once it is closed
// and every record in it has been confirmed. What is left on disk when the
// engine starts is sent again: a record already in NATS is dropped there
// by its message id, and the rater stores a leg once whatever it is sent.
//
// One goroutine appends and one reads; acknowledgements may come from any
// number. No file is read or written while the lock is held.
type Journal struct {
	dir string
	// SegmentBytes and SegmentAge bound a segment: the one being written
	// is closed once it holds this much or is this old, whichever comes
	// first, so what a restart sends again is bounded too.
	SegmentBytes int64
	SegmentAge   time.Duration
	maxBytes     int64

	mu   sync.Mutex
	segs []*segment // oldest first; the last one is being written to

	lock  *os.File     // held while the journal is open: one process a directory
	bytes atomic.Int64 // committed bytes in the segments on disk
	depth atomic.Int64 // records on disk not yet confirmed
	wake  chan struct{}

	// The writer's own.
	w       *os.File
	created time.Time

	// The reader's own.
	rseg *segment
	rf   *os.File
	rbuf []byte
	roff int64 // file offset just past rbuf
}

type segment struct {
	seq    uint64
	path   string
	size   int64 // bytes committed (written and synced)
	closed bool  // no more will be written to it
	read   bool  // the reader has handed out everything in it
	out    int   // records handed out and not yet confirmed
	gone   bool
	replay bool // found on disk when the journal was opened
}

// JournalEntry is one record handed out by Next, to be confirmed with Ack.
type JournalEntry struct {
	Data []byte
	seg  *segment
}

// ErrJournalFull means the journal holds as much as it may; the records
// stay where they are (in memory) and are offered again.
var ErrJournalFull = errors.New("cdr journal is full")

const (
	frameHeader = 8       // length and CRC-32C of the record, little-endian
	maxFrame    = 1 << 20 // a record longer than this is not one
	// DefaultJournalSegment and DefaultJournalAge are the segment bounds.
	DefaultJournalSegment = 1 << 20
	DefaultJournalAge     = time.Minute
	readChunk             = 256 << 10
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

func segName(seq uint64) string { return fmt.Sprintf("cdr-%020d.log", seq) }

// OpenJournal opens the journal in dir, creating it if need be, and makes
// what an earlier process left there the first records Next hands out. A
// record cut short when that process died (its last write) was never
// confirmed to anyone and is dropped; so is the rest of a segment after a
// record that fails its checksum.
func OpenJournal(dir string, maxBytes int64) (*Journal, error) {
	if maxBytes < DefaultJournalSegment {
		return nil, fmt.Errorf("cdr journal: size %d is below one segment (%d bytes)", maxBytes, DefaultJournalSegment)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("cdr journal: %w", err)
	}
	// Two processes on one journal would send and remove each other's
	// records: the second one is refused.
	lock, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o640)
	if err != nil {
		return nil, fmt.Errorf("cdr journal: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("cdr journal: %s is in use by another process: %w", dir, err)
	}
	j, err := openJournal(dir, maxBytes)
	if err != nil {
		lock.Close()
		return nil, err
	}
	j.lock = lock
	return j, nil
}

func openJournal(dir string, maxBytes int64) (*Journal, error) {
	j := &Journal{dir: dir, SegmentBytes: DefaultJournalSegment, SegmentAge: DefaultJournalAge,
		maxBytes: maxBytes, wake: make(chan struct{}, 1)}
	names, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cdr journal: %w", err)
	}
	var seqs []uint64
	for _, n := range names {
		s, ok := strings.CutPrefix(n.Name(), "cdr-")
		if s, ok2 := strings.CutSuffix(s, ".log"); ok && ok2 && !n.IsDir() {
			if seq, err := strconv.ParseUint(s, 10, 64); err == nil {
				seqs = append(seqs, seq)
			}
		}
	}
	sort.Slice(seqs, func(a, b int) bool { return seqs[a] < seqs[b] })
	var next uint64 = 1
	for _, seq := range seqs {
		next = seq + 1
		path := filepath.Join(dir, segName(seq))
		size, n, err := recoverSegment(path)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			if err := os.Remove(path); err != nil {
				return nil, fmt.Errorf("cdr journal: %w", err)
			}
			continue
		}
		j.segs = append(j.segs, &segment{seq: seq, path: path, size: size, closed: true, replay: true})
		j.bytes.Add(size)
		j.depth.Add(int64(n))
	}
	if err := j.startSegment(next); err != nil {
		return nil, err
	}
	if n := j.depth.Load(); n > 0 {
		log.Printf("engine: cdr journal: %d records left by the previous run are sent again", n)
	}
	return j, nil
}

// recoverSegment checks a segment left on disk and cuts it after its last
// whole record. It returns the size kept and how many records it holds.
func recoverSegment(path string) (int64, int, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return 0, 0, fmt.Errorf("cdr journal: %w", err)
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, readChunk)
	var off int64
	n := 0
	hdr := make([]byte, frameHeader)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			if err == io.EOF {
				return off, n, nil
			}
			if err != io.ErrUnexpectedEOF {
				return 0, 0, fmt.Errorf("cdr journal: reading %s: %w", path, err)
			}
			break
		}
		size := binary.LittleEndian.Uint32(hdr)
		if size == 0 || size > maxFrame {
			break
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			if err != io.ErrUnexpectedEOF && err != io.EOF {
				return 0, 0, fmt.Errorf("cdr journal: reading %s: %w", path, err)
			}
			break
		}
		if crc32.Checksum(data, crcTable) != binary.LittleEndian.Uint32(hdr[4:]) {
			break
		}
		off += frameHeader + int64(size)
		n++
	}
	// What follows the last whole record was never confirmed as written.
	mJournalTorn.Inc()
	log.Printf("engine: cdr journal: %s: dropping a cut-off or damaged tail after %d records", filepath.Base(path), n)
	if err := f.Truncate(off); err != nil {
		return 0, 0, fmt.Errorf("cdr journal: %w", err)
	}
	return off, n, f.Sync()
}

// startSegment opens a new segment for writing. Writer only.
func (j *Journal) startSegment(seq uint64) error {
	path := filepath.Join(j.dir, segName(seq))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return fmt.Errorf("cdr journal: %w", err)
	}
	// The new name must outlive a crash as surely as what is written there.
	if d, err := os.Open(j.dir); err == nil {
		d.Sync()
		d.Close()
	}
	old := j.w
	j.w, j.created = f, time.Now()
	j.mu.Lock()
	var prev *segment
	if len(j.segs) > 0 && !j.segs[len(j.segs)-1].closed {
		prev = j.segs[len(j.segs)-1]
		prev.closed = true
	}
	j.segs = append(j.segs, &segment{seq: seq, path: path})
	rm := j.collect(prev)
	j.mu.Unlock()
	if old != nil {
		old.Close()
	}
	j.remove(rm)
	j.signal()
	return nil
}

// current is the segment being written. Writer only.
func (j *Journal) current() *segment {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.segs[len(j.segs)-1]
}

// Append writes records and syncs them to disk; they are committed, and
// handed out by Next, only once it returns nil. Writer only.
func (j *Journal) Append(recs [][]byte) error {
	var buf []byte
	for _, r := range recs {
		var hdr [frameHeader]byte
		binary.LittleEndian.PutUint32(hdr[:], uint32(len(r)))
		binary.LittleEndian.PutUint32(hdr[4:], crc32.Checksum(r, crcTable))
		buf = append(append(buf, hdr[:]...), r...)
	}
	cur := j.current()
	if j.bytes.Load()+int64(len(buf)) > j.maxBytes {
		// Confirmed records in the segment being written free nothing
		// until it is closed.
		if cur.size > 0 && !cur.closed {
			if err := j.startSegment(cur.seq + 1); err != nil {
				return err
			}
		}
		return ErrJournalFull
	}
	if cur.closed {
		// An earlier failure left it in an unknown state; go on in a new one.
		if err := j.startSegment(cur.seq + 1); err != nil {
			return err
		}
		cur = j.current()
	}
	_, err := j.w.Write(buf)
	if err == nil {
		err = j.w.Sync()
	}
	if err != nil {
		// Put the segment back the way it was committed. If that fails
		// too, nothing more goes in it: the reader stops at what was
		// committed, and a restart cuts off the rest.
		if j.w.Truncate(cur.size) != nil {
			j.mu.Lock()
			cur.closed = true
			rm := j.collect(cur)
			j.mu.Unlock()
			j.remove(rm)
		}
		return fmt.Errorf("cdr journal: %w", err)
	}
	j.mu.Lock()
	cur.size += int64(len(buf))
	j.mu.Unlock()
	j.bytes.Add(int64(len(buf)))
	j.depth.Add(int64(len(recs)))
	j.signal()
	return j.Tidy()
}

// Tidy closes the segment being written once it is big or old enough, so
// that it can be removed when its records are confirmed. Writer only.
func (j *Journal) Tidy() error {
	cur := j.current()
	if cur.closed || cur.size >= j.SegmentBytes || (cur.size > 0 && time.Since(j.created) >= j.SegmentAge) {
		return j.startSegment(cur.seq + 1)
	}
	return nil
}

func (j *Journal) signal() {
	select {
	case j.wake <- struct{}{}:
	default:
	}
}

// Next hands out the oldest record not handed out yet, waiting for one
// until stop is closed (then ok is false). Reader only.
func (j *Journal) Next(stop <-chan struct{}) (JournalEntry, bool) {
	for {
		j.mu.Lock()
		if j.rseg == nil || j.rseg.gone {
			j.rseg = nil
			for _, s := range j.segs {
				if !s.read {
					j.rseg = s
					break
				}
			}
		}
		seg := j.rseg
		size, closed := seg.size, seg.closed
		j.mu.Unlock()

		if rec, ok := j.parse(seg); ok {
			j.mu.Lock()
			seg.out++
			j.mu.Unlock()
			if closed && len(j.rbuf) == 0 && j.roff == size {
				// The last of a closed segment: it can go as soon as
				// this record is confirmed.
				j.finishRead(seg)
			}
			return JournalEntry{Data: rec, seg: seg}, true
		}
		if j.roff < size {
			if err := j.fill(seg, size); err != nil {
				// The records stay on disk; try again in a while (the
				// alert on the journal's backlog tells the operator).
				mJournalErrors["read"].Inc()
				log.Printf("engine: cdr journal: %v", err)
				if j.rf != nil {
					j.rf.Close()
					j.rf = nil
				}
				select {
				case <-time.After(time.Second):
				case <-stop:
					return JournalEntry{}, false
				}
			}
			continue
		}
		if closed {
			j.finishRead(seg)
			continue
		}
		select {
		case <-j.wake:
		case <-stop:
			return JournalEntry{}, false
		}
	}
}

// parse takes the next whole record out of what has been read of seg.
func (j *Journal) parse(seg *segment) ([]byte, bool) {
	if len(j.rbuf) < frameHeader {
		return nil, false
	}
	size := int(binary.LittleEndian.Uint32(j.rbuf))
	if len(j.rbuf) < frameHeader+size {
		return nil, false
	}
	rec := make([]byte, size)
	copy(rec, j.rbuf[frameHeader:frameHeader+size])
	ok := crc32.Checksum(rec, crcTable) == binary.LittleEndian.Uint32(j.rbuf[4:])
	j.rbuf = j.rbuf[frameHeader+size:]
	if !ok {
		// Committed data that reads back wrong: the disk is failing.
		mJournalErrors["read"].Inc()
		log.Printf("engine: cdr journal: a record in %s fails its checksum; skipped", filepath.Base(seg.path))
		return j.parse(seg)
	}
	return rec, true
}

// fill reads more of seg, up to what is committed.
func (j *Journal) fill(seg *segment, size int64) error {
	if j.rf == nil {
		f, err := os.Open(seg.path)
		if err != nil {
			return err
		}
		j.rf = f
	}
	if len(j.rbuf) >= frameHeader {
		// What is committed ends with a whole record: a length no record
		// has means the disk gave back something else.
		if l := binary.LittleEndian.Uint32(j.rbuf); l == 0 || l > maxFrame {
			return fmt.Errorf("%s reads back damaged at offset %d", filepath.Base(seg.path), j.roff-int64(len(j.rbuf)))
		}
	}
	n := min(size-j.roff, readChunk)
	buf := make([]byte, len(j.rbuf), len(j.rbuf)+int(n))
	copy(buf, j.rbuf)
	got, err := j.rf.ReadAt(buf[len(buf):len(buf)+int(n)], j.roff)
	j.rbuf, j.roff = buf[:len(buf)+got], j.roff+int64(got)
	if err != nil && !(err == io.EOF && int64(got) == n) {
		return fmt.Errorf("reading %s: %w", filepath.Base(seg.path), err)
	}
	return nil
}

// finishRead moves the reader past seg.
func (j *Journal) finishRead(seg *segment) {
	if j.rf != nil {
		j.rf.Close()
	}
	j.rf, j.rbuf, j.roff = nil, nil, 0
	j.mu.Lock()
	seg.read = true
	j.rseg = nil
	rm := j.collect(seg)
	j.mu.Unlock()
	j.remove(rm)
}

// Ack confirms that NATS has stored a record handed out by Next.
func (j *Journal) Ack(e JournalEntry) {
	j.mu.Lock()
	e.seg.out--
	rm := j.collect(e.seg)
	j.mu.Unlock()
	j.depth.Add(-1)
	if e.seg.replay {
		mJournalReplayed.Inc()
	}
	j.remove(rm)
}

// collect takes seg out of the journal when nothing more is to come from
// it, and returns the file to remove. Lock held.
func (j *Journal) collect(seg *segment) string {
	if seg == nil || seg.gone || !seg.closed || !seg.read || seg.out > 0 {
		return ""
	}
	seg.gone = true
	for i, s := range j.segs {
		if s == seg {
			j.segs = append(j.segs[:i], j.segs[i+1:]...)
			break
		}
	}
	j.bytes.Add(-seg.size)
	return seg.path
}

func (j *Journal) remove(path string) {
	if path == "" {
		return
	}
	// A segment that cannot be removed is sent again after a restart:
	// harmless, but it takes room and time.
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		mJournalErrors["remove"].Inc()
		log.Printf("engine: cdr journal: %v", err)
	}
}

// Depth is the number of records on disk not yet confirmed by NATS.
func (j *Journal) Depth() int64 { return j.depth.Load() }

// Bytes is the room the journal takes on disk; MaxBytes the most it may.
func (j *Journal) Bytes() int64    { return j.bytes.Load() }
func (j *Journal) MaxBytes() int64 { return j.maxBytes }

// Close closes the files. Records not confirmed stay on disk for the next
// process.
func (j *Journal) Close() error {
	if j.rf != nil {
		j.rf.Close()
	}
	err := j.w.Close()
	if j.lock != nil {
		j.lock.Close() // and with it the lock
	}
	return err
}
