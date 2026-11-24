// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"softswitch/core/pkg/rating"
	"softswitch/core/pkg/routing"
)

// benchSnapshot is the test snapshot without limits and with funds that do
// not run out, so that every call of a benchmark is admitted.
func benchSnapshot() *routing.Snapshot {
	s := testSnapshot()
	cl := s.Clients[1]
	cl.MaxCalls, cl.Balance = 0, 1_000_000_000*rating.Micro
	s.Clients[1] = cl
	for id, c := range s.Connections {
		c.MaxCalls = 0
		s.Connections[id] = c
	}
	s.Trunks[0].Codecs = []string{"PCMA", "PCMU"}
	s.Trunks = append(s.Trunks, routing.Trunk{ID: 7, ClientID: 1, Username: "pbx", HA1: "x", MaxContacts: 2, MaxCalls: 100000, Codecs: []string{"PCMA"}})
	return s
}

// BenchmarkWireVendor is the route of an ordinary call as the SIP core is
// told it.
func BenchmarkWireVendor(b *testing.B) {
	e := New(nil, nil, nil)
	e.snap.Store(benchSnapshot())
	c := &Call{ClientID: 1, TrunkID: 1, Src: "100", Routes: []callRoute{{ConnectionID: 1, URI: "sip:905321112233@a:5060;transport=udp"}}}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if w := e.wire(c, c.Routes[0]); w.URI == "" {
			b.Fatal("no route")
		}
	}
}

// BenchmarkDeliverVendor is the look for routes to a client's trunk in a
// decision that has none: what every ordinary call pays for the feature.
func BenchmarkDeliverVendor(b *testing.B) {
	e := New(nil, nil, nil)
	snap := benchSnapshot()
	d := snap.AuthorizeFrom(routing.Source{IP: "10.0.0.1", Caller: "100", At: time.Now()}, "905321112233")
	if !d.Allowed {
		b.Fatalf("decision: %+v", d)
	}
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := deliverForBench(e, ctx, snap, d); err != nil {
			b.Fatal(err)
		}
	}
}

func benchEngine(b *testing.B) (*Engine, *redis.Client) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		b.Skip("TEST_REDIS_URL not set")
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		b.Fatal(err)
	}
	rdb := redis.NewClient(opt)
	b.Cleanup(func() { rdb.FlushDB(context.Background()); rdb.Close() })
	if err := rdb.FlushDB(context.Background()).Err(); err != nil {
		b.Fatal(err)
	}
	e := New(func(context.Context) (*routing.Snapshot, error) { return benchSnapshot(), nil }, NewRedisCalls(rdb), &memSink{})
	if err := e.Reload(context.Background()); err != nil {
		b.Fatal(err)
	}
	e.Registry = NewRedisRegistry(rdb)
	return e, rdb
}

// BenchmarkAuthorizeVendor is a whole decision of an ordinary call, with
// its one trip to Redis.
func BenchmarkAuthorizeVendor(b *testing.B) {
	e, _ := benchEngine(b)
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if a := e.Authorize(ctx, call("bv"+strconv.Itoa(i))); !a.Allowed {
			b.Fatalf("refused: %+v", a)
		}
	}
}

// BenchmarkAuthorizeToTrunk is a whole decision of a call delivered to a
// registered trunk: the registrations are read, then the call admitted.
func BenchmarkAuthorizeToTrunk(b *testing.B) {
	e, _ := benchEngine(b)
	ctx := context.Background()
	in := reg("pbx", "<sip:pbx@203.0.113.9:5060>", 3600)
	if out := e.Register(ctx, in); !out.Allowed {
		b.Fatalf("register: %+v", out)
	}
	e.Adjust = func(ci CallInfo, d routing.Decision) routing.Decision {
		d.Routes = []routing.Route{routing.ToTrunk(7, ci.Dst)}
		return d
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if a := e.Authorize(ctx, call("bt"+strconv.Itoa(i))); !a.Allowed {
			b.Fatalf("refused: %+v", a)
		}
	}
}
