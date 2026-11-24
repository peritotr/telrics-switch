// SPDX-License-Identifier: AGPL-3.0-only

// Package sysinfo reads what the machine is doing: processor, load, memory,
// disks and network. It reads the Linux kernel's own figures under /proc,
// which inside a container still describe the whole machine; on a system
// without them (a developer's Mac) only the disks are reported.
package sysinfo

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Sample is the state of the machine at one moment.
type Sample struct {
	At time.Time `json:"at"`
	// CPUPermille is the share of processor time in use since the sample
	// before, in thousandths of all cores together.
	CPUPermille int `json:"cpu_permille"`
	// Load is the one-minute load average in thousandths.
	LoadMilli    int64 `json:"load_milli"`
	MemTotal     int64 `json:"mem_total"`     // bytes
	MemAvailable int64 `json:"mem_available"` // bytes a new program could use
	// Bytes a second over the machine's own network interfaces.
	RxBps int64 `json:"rx_bps"`
	TxBps int64 `json:"tx_bps"`
}

// Disk is the space of the file system holding a path.
type Disk struct {
	Path  string `json:"path"`
	Total int64  `json:"total"`
	Free  int64  `json:"free"` // what an ordinary program may still write
}

// State is what a Collector knows.
type State struct {
	// Supported is false where the kernel's figures cannot be read; only
	// Disks is filled then.
	Supported bool     `json:"supported"`
	Cores     int      `json:"cores"`
	UptimeSec int64    `json:"uptime_sec"`
	Latest    *Sample  `json:"latest"`
	History   []Sample `json:"history"` // oldest first
	Disks     []Disk   `json:"disks"`
}

// Collector samples the machine at an interval and keeps the last samples.
type Collector struct {
	proc  string // where the kernel's figures are; "/proc"
	paths []string
	keep  int

	mu      sync.Mutex
	history []Sample
	cores   int
	last    counters
	lastAt  time.Time
	ok      bool
}

// counters are the kernel's running totals a rate is taken from.
type counters struct {
	busy, total uint64 // processor ticks
	rx, tx      uint64 // bytes
}

// New makes a collector that keeps `keep` samples and reports the disks
// holding the given paths.
func New(keep int, paths ...string) *Collector {
	return &Collector{proc: "/proc", paths: paths, keep: keep}
}

// Run samples every interval until ctx ends.
func (c *Collector) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		c.sample(time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *Collector) sample(now time.Time) {
	stat, err1 := os.ReadFile(c.proc + "/stat")
	mem, err2 := os.ReadFile(c.proc + "/meminfo")
	load, err3 := os.ReadFile(c.proc + "/loadavg")
	net, _ := os.ReadFile(c.proc + "/net/dev")
	c.mu.Lock()
	defer c.mu.Unlock()
	if err1 != nil || err2 != nil || err3 != nil {
		c.ok = false
		return
	}
	var cur counters
	cur.busy, cur.total, c.cores = parseCPU(string(stat))
	cur.rx, cur.tx = parseNet(string(net))
	s := Sample{At: now.UTC(), LoadMilli: parseLoad(string(load))}
	s.MemTotal, s.MemAvailable = parseMem(string(mem))
	// Rates need a sample before this one; counters that went backwards
	// (a restart, an interface that left) give no rate.
	if c.ok && cur.total > c.last.total {
		s.CPUPermille = int((cur.busy - min(cur.busy, c.last.busy)) * 1000 / (cur.total - c.last.total))
		if sec := now.Sub(c.lastAt).Seconds(); sec > 0 && cur.rx >= c.last.rx && cur.tx >= c.last.tx {
			s.RxBps = int64(float64(cur.rx-c.last.rx) / sec)
			s.TxBps = int64(float64(cur.tx-c.last.tx) / sec)
		}
		c.history = append(c.history, s)
		if len(c.history) > c.keep {
			c.history = c.history[len(c.history)-c.keep:]
		}
	}
	c.last, c.lastAt, c.ok = cur, now, true
}

// State reports what has been collected, with the disks as they are now.
func (c *Collector) State() State {
	st := State{History: []Sample{}}
	for _, p := range c.paths {
		var fs syscall.Statfs_t
		if err := syscall.Statfs(p, &fs); err == nil {
			st.Disks = append(st.Disks, Disk{Path: p, Total: int64(fs.Blocks) * int64(fs.Bsize), Free: int64(fs.Bavail) * int64(fs.Bsize)})
		}
	}
	if up, err := os.ReadFile(c.proc + "/uptime"); err == nil {
		if f := strings.Fields(string(up)); len(f) > 0 {
			whole, _, _ := strings.Cut(f[0], ".")
			st.UptimeSec, _ = strconv.ParseInt(whole, 10, 64)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st.Supported, st.Cores = c.ok, c.cores
	st.History = append(st.History, c.history...)
	if n := len(c.history); n > 0 {
		latest := c.history[n-1]
		st.Latest = &latest
	}
	return st
}

// parseCPU reads /proc/stat: ticks spent busy and in all, and the number of
// processors listed.
func parseCPU(stat string) (busy, total uint64, cores int) {
	sc := bufio.NewScanner(strings.NewReader(stat))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || !strings.HasPrefix(f[0], "cpu") {
			continue
		}
		if f[0] != "cpu" {
			cores++
			continue
		}
		// user nice system idle iowait irq softirq steal …; guest time is
		// already inside user.
		for i, v := range f[1:] {
			if i > 7 {
				break
			}
			n, _ := strconv.ParseUint(v, 10, 64)
			total += n
			if i != 3 && i != 4 { // not idle, not waiting for a disk
				busy += n
			}
		}
	}
	return busy, total, cores
}

// parseMem reads /proc/meminfo, in bytes.
func parseMem(meminfo string) (total, available int64) {
	sc := bufio.NewScanner(strings.NewReader(meminfo))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		kb, _ := strconv.ParseInt(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = kb * 1024
		case "MemAvailable:":
			available = kb * 1024
		}
	}
	return total, available
}

// parseLoad reads the one-minute average of /proc/loadavg, in thousandths.
func parseLoad(loadavg string) int64 {
	f := strings.Fields(loadavg)
	if len(f) == 0 {
		return 0
	}
	whole, frac, _ := strings.Cut(f[0], ".")
	frac = (frac + "000")[:3]
	w, _ := strconv.ParseInt(whole, 10, 64)
	m, _ := strconv.ParseInt(frac, 10, 64)
	return w*1000 + m
}

// parseNet adds up the bytes of /proc/net/dev over the machine's own
// interfaces: not the loopback, and not the bridges and virtual pairs
// containers are wired with, which would count the same traffic again.
func parseNet(dev string) (rx, tx uint64) {
	sc := bufio.NewScanner(strings.NewReader(dev))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "lo" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(f[0], 10, 64)
		t, _ := strconv.ParseUint(f[8], 10, 64)
		rx, tx = rx+r, tx+t
	}
	return rx, tx
}
