// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"bytes"
	"context"
	"crypto/subtle"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// idle stands in for the state store: no calls in progress. The requests
// made here ask it nothing else.
type idle struct{ Calls }

func (idle) Count(_ context.Context, _ time.Time, keys []string) ([]int64, error) {
	return make([]int64, len(keys)), nil
}

func authEngine(t *testing.T, token string) *httptest.Server {
	t.Helper()
	e := New(nil, idle{}, &memSink{})
	e.snap.Store(testSnapshot())
	e.Token = token
	srv := httptest.NewServer(e.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func status(t *testing.T, srv *httptest.Server, method, path, authorization string) int {
	t.Helper()
	body := strings.NewReader("")
	if method == "POST" {
		body = strings.NewReader(`{"v":1,"src_ip":"203.0.113.9","username":"nobody","reason":"flood","seconds":120}`)
	}
	req, _ := http.NewRequest(method, srv.URL+path, body)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// Every path of the interface but the two open ones; each answers 200 to
// the request status() makes when it is let through.
var guarded = []struct{ method, path string }{
	{"POST", "/v1/source"}, {"POST", "/v1/credentials"}, {"POST", "/v1/ban"}, {"GET", "/v1/bans"},
	{"GET", "/v1/stats"}, {"GET", "/v1/live"},
}

func TestTokenRequired(t *testing.T) {
	const token = "s3cret-Token_0123456789"
	var logged bytes.Buffer
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)
	refusedLogged.Store(0)
	compared := 0
	sameBytes = func(a, b []byte) int {
		compared++
		if len(a) != len(b) {
			t.Errorf("compared values of different lengths (%d, %d): the time taken would depend on what was sent", len(a), len(b))
		}
		return subtle.ConstantTimeCompare(a, b)
	}
	defer func() { sameBytes = subtle.ConstantTimeCompare }()

	srv := authEngine(t, token)
	before := mUnauthorized.Value()
	refusals := 0
	for _, p := range guarded {
		for _, wrong := range []string{"", "Bearer not-it", "Bearer " + token + "x", "Bearer " + token[:len(token)-1], "Basic " + token, token, "Bearer", "Bearer "} {
			if got := status(t, srv, p.method, p.path, wrong); got != 401 {
				t.Errorf("%s %s with %q: got %d, want 401", p.method, p.path, wrong, got)
			}
			refusals++
		}
		for _, right := range []string{"Bearer " + token, "bearer " + token} {
			if got := status(t, srv, p.method, p.path, right); got/100 != 2 {
				t.Errorf("%s %s with the token: got %d, want 2xx", p.method, p.path, got)
			}
		}
	}
	// Paths that would act on a call are refused the same way, before
	// anything is read from the request.
	for _, path := range []string{"/v1/authorize", "/v1/events", "/v1/hangup", "/v1/unban"} {
		if got := status(t, srv, "POST", path, ""); got != 401 {
			t.Errorf("POST %s without a token: got %d, want 401", path, got)
		}
		refusals++
	}
	if got := status(t, srv, "GET", "/v1/calls", "Bearer nope"); got != 401 {
		t.Errorf("GET /v1/calls with a wrong token: got %d, want 401", got)
	}
	refusals++
	// A path that does not exist says nothing about itself either.
	if got := status(t, srv, "GET", "/v1/nothing", ""); got != 401 {
		t.Errorf("unknown path without a token: got %d, want 401", got)
	}
	refusals++
	// Open on purpose: counters for Prometheus and the health check.
	for _, path := range []string{"/metrics", "/healthz"} {
		if got := status(t, srv, "GET", path, ""); got != 200 {
			t.Errorf("GET %s without a token: got %d, want 200", path, got)
		}
	}
	if compared < refusals {
		t.Errorf("constant-time comparison ran %d times for %d refusals", compared, refusals)
	}
	if got := mUnauthorized.Value() - before; got != uint64(refusals) {
		t.Errorf("refusals counted: got %d, want %d", got, refusals)
	}
	// Refusals are logged (sparingly) and never with what was sent.
	out := logged.String()
	if n := strings.Count(out, "refused"); n != 1 {
		t.Errorf("refusals logged %d times in a minute, want 1:\n%s", n, out)
	}
	if strings.Contains(out, token) || strings.Contains(out, "not-it") {
		t.Errorf("a token was written to the log:\n%s", out)
	}
}

func TestNoTokenLeavesTheInterfaceOpen(t *testing.T) {
	srv := authEngine(t, "")
	for _, p := range guarded {
		for _, auth := range []string{"", "Bearer anything"} {
			if got := status(t, srv, p.method, p.path, auth); got/100 != 2 {
				t.Errorf("%s %s with %q and no token set: got %d, want 2xx", p.method, p.path, auth, got)
			}
		}
	}
}

// Serve says so when it runs without a token, and will not take one that a
// caller could not send.
func TestServeTokenChecks(t *testing.T) {
	if err := CheckToken("0123456789abcdefABCDEF-._~+/="); err != nil {
		t.Errorf("a bearer token was refused: %v", err)
	}
	if err := CheckToken(""); err != nil {
		t.Errorf("no token was refused: %v", err)
	}
	for _, bad := range []string{"with space", `quo"te`, "new\nline", "ünicode", "semi;colon"} {
		if CheckToken(bad) == nil {
			t.Errorf("%q was accepted as a token", bad)
		}
		err := Serve(context.Background(), Config{Token: bad, RedisURL: "redis://127.0.0.1:1/0"})
		if err == nil || strings.Contains(err.Error(), bad) {
			t.Errorf("Serve with token %q: %v", bad, err)
		}
	}
	// The warning comes before anything else is tried; this Serve stops at
	// its unusable Redis address.
	var logged bytes.Buffer
	log.SetOutput(&logged)
	defer log.SetOutput(os.Stderr)
	Serve(context.Background(), Config{RedisURL: "not a url"})
	if !strings.Contains(logged.String(), "WARNING") || !strings.Contains(logged.String(), "ENGINE_TOKEN") {
		t.Errorf("no warning when serving without a token: %q", logged.String())
	}
	logged.Reset()
	Serve(context.Background(), Config{RedisURL: "not a url", Token: "abc"})
	if strings.Contains(logged.String(), "WARNING") || strings.Contains(logged.String(), "abc") {
		t.Errorf("serving with a token logged %q", logged.String())
	}
}
