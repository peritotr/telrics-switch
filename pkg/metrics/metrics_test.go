// SPDX-License-Identifier: AGPL-3.0-only
package metrics

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"softswitch/core/pkg/version"
)

func TestWrite(t *testing.T) {
	a := NewCounter("t_calls_total", "Calls.", "result=allowed")
	b := NewCounter("t_calls_total", "Calls.", "result=refused")
	a.Add(3)
	b.Inc()
	NewGauge("t_age_seconds", "Age.", func() (float64, bool) { return 1.5, true })
	NewGauge("t_blank", "Blank.", func() (float64, bool) { return 0, false })
	h := NewHistogram("t_seconds", "Time.", []float64{0.001, 0.01})
	h.Observe(500 * time.Microsecond)
	h.Observe(time.Millisecond) // on the bound: counted in it
	h.Observe(5 * time.Millisecond)
	h.Observe(time.Second)

	var out strings.Builder
	Write(&out)
	for _, want := range []string{
		"# TYPE t_calls_total counter\nt_calls_total{result=\"allowed\"} 3\nt_calls_total{result=\"refused\"} 1\n",
		"# TYPE t_age_seconds gauge\nt_age_seconds 1.5\n",
		"# TYPE t_blank gauge\n#",
		"t_seconds_bucket{le=\"0.001\"} 2\nt_seconds_bucket{le=\"0.01\"} 3\nt_seconds_bucket{le=\"+Inf\"} 4\nt_seconds_sum 1.0065\nt_seconds_count 4\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestBuildInfo(t *testing.T) {
	var out strings.Builder
	Write(&out)
	want := "softswitch_build_info{version=\"" + version.Get() + "\",goversion=\"" + runtime.Version() + "\"} 1\n"
	if !strings.Contains(out.String(), want) {
		t.Errorf("output lacks %q", want)
	}
}

func TestDuplicatePanics(t *testing.T) {
	NewCounter("t_dup_total", "Dup.")
	defer func() {
		if recover() == nil {
			t.Error("registering the same series twice did not panic")
		}
	}()
	NewCounter("t_dup_total", "Dup.")
}
