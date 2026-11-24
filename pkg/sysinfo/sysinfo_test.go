// SPDX-License-Identifier: AGPL-3.0-only

package sysinfo

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	path := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollector(t *testing.T) {
	dir := t.TempDir()
	c := New(2, dir, "/nowhere/at/all")
	c.proc = dir
	now := time.Unix(1_800_000_000, 0)

	// Nothing to read: not supported, but the disk is still reported.
	c.sample(now)
	if st := c.State(); st.Supported || st.Latest != nil || len(st.Disks) != 1 || st.Disks[0].Total <= 0 || st.Disks[0].Free > st.Disks[0].Total {
		t.Fatalf("without /proc: %+v", st)
	}

	set := func(busy, idle, rx, tx int) {
		write(t, dir, "stat", "cpu  "+itoa(busy)+" 0 0 "+itoa(idle)+" 0 0 0 0 0 0\ncpu0 1 0 0 1 0 0 0 0 0 0\ncpu1 1 0 0 1 0 0 0 0 0 0\nintr 5\n")
		write(t, dir, "meminfo", "MemTotal:       16000000 kB\nMemFree:          100000 kB\nMemAvailable:    4000000 kB\n")
		write(t, dir, "loadavg", "1.25 0.80 0.50 2/300 4242\n")
		write(t, dir, "uptime", "86400.52 100000.00\n")
		write(t, dir, "net/dev", "Inter-|   Receive                                                |  Transmit\n face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n"+
			"    lo: 999999 1 0 0 0 0 0 0 999999 1 0 0 0 0 0 0\n  eth0: "+itoa(rx)+" 1 0 0 0 0 0 0 "+itoa(tx)+" 1 0 0 0 0 0 0\n"+
			"veth12: 555555 1 0 0 0 0 0 0 555555 1 0 0 0 0 0 0\ndocker0: 7 1 0 0 0 0 0 0 7 1 0 0 0 0 0 0\nbr-ab12: 7 1 0 0 0 0 0 0 7 1 0 0 0 0 0 0\n")
	}
	// The first reading gives totals only; rates come with the second.
	set(1000, 9000, 1_000_000, 2_000_000)
	c.sample(now)
	if st := c.State(); !st.Supported || st.Latest != nil || st.Cores != 2 || st.UptimeSec != 86400 {
		t.Fatalf("first reading: %+v", st)
	}
	// Ten seconds on: 250 of 1000 ticks busy, 5 MB in, 1 MB out.
	set(1250, 9750, 6_000_000, 3_000_000)
	c.sample(now.Add(10 * time.Second))
	st := c.State()
	if st.Latest == nil {
		t.Fatal("no sample after the second reading")
	}
	if s := *st.Latest; s.CPUPermille != 250 || s.LoadMilli != 1250 || s.MemTotal != 16_000_000*1024 || s.MemAvailable != 4_000_000*1024 || s.RxBps != 500_000 || s.TxBps != 100_000 {
		t.Errorf("sample: %+v", s)
	}
	// Only the newest samples are kept; counters that fall back give no rate
	// and no nonsense.
	set(1500, 10500, 7_000_000, 4_000_000)
	c.sample(now.Add(20 * time.Second))
	set(1600, 11400, 10, 10) // the interface was reset
	c.sample(now.Add(30 * time.Second))
	st = c.State()
	if len(st.History) != 2 || st.Latest.CPUPermille != 100 || st.Latest.RxBps != 0 || st.Latest.TxBps != 0 {
		t.Errorf("history: %+v", st.History)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
