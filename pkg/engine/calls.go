// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"softswitch/core/pkg/numplan"
	"softswitch/core/pkg/rating"
	"softswitch/core/pkg/routing"
)

// Admission results below zero; anything else is the index of the route
// that was admitted.
const (
	limitCalls   = -1
	limitCPS     = -2
	limitVendors = -3
	limitFunds   = -4
	limitSystem  = -5
	limitBundle  = -6 // the bundle has fewer seconds free than were asked of it
	limitTrunk   = -7 // the trunk's own limit on calls at once
	limitTrunkPS = -8 // the trunk's own limit on calls a second
)

// setupSlack is how long a call may take to be answered on top of its
// allowed duration before its state and its place in the counters expire.
const setupSlack = 300

// settleWait is how long the charge of a finished call stays held while
// nothing confirms that it reached the balance.
const settleWait = 24 * time.Hour

// expiryGrace is how long a call's state outlives the moment it expires,
// for the engine to write its record (ADR-0029). Engines that are all down
// for longer lose the records of the calls that expired meanwhile.
const expiryGrace = 24 * time.Hour

// expiryLease is how long a claimed expired call is left alone.
const expiryLease = 30 * time.Second

// Funds is what a client may spend: balance plus credit as the snapshot saw
// them, and the moment (store clock, ms) that snapshot started loading.
// Holds settled before that moment are already part of Total.
type Funds struct {
	Total    int64
	SyncedMs int64
}

// Free is what was free to hold before an operation: money, and seconds of
// the call's bundle.
type Free struct {
	Money, Bundle int64
	// TranscodeFull counts the routes passed over because the calls
	// transcoded at once were at their limit.
	TranscodeFull int64
}

// Outcome of an attempt to change what a call holds.
const (
	extDone        = 1
	extShort       = 0  // not enough money
	extChanged     = 2  // another engine has just dealt with the call
	extBundleShort = 3  // not enough seconds in the bundle
	extGone        = -1 // the call is over
)

// Calls keeps per-call state, the concurrency and rate counters and the
// money held for calls in progress.
type Calls interface {
	// Admit checks the funds and the limits, picks the first route with
	// capacity, holds c.BundleSec seconds of the call's bundle and the cost
	// of the rest of c.Reserved seconds, and stores the call. It returns the
	// route index or one of the limit* values, and what was free before this
	// call. bf is the bundle as the snapshot saw it, in seconds.
	Admit(ctx context.Context, id string, c *Call, d routing.Decision, f, bf Funds, now time.Time) (int, Free, error)
	// Next moves the call to the following route with capacity; -1 if none.
	// It also returns how many routes were passed over because the calls
	// transcoded at once were at their limit.
	Next(ctx context.Context, id string, c *Call, now time.Time) (int, int64, error)
	// Answer records the answer time and schedules the first extension lead
	// before the reserved time runs out.
	Answer(ctx context.Context, id string, now time.Time, lead time.Duration) error
	// Get returns nil for a call that is unknown or already finished.
	Get(ctx context.Context, id string) (*Call, error)
	// Release replaces what the call holds with what it cost, in money and
	// in seconds of its bundle. Both stay held until the rater has settled
	// them and a snapshot shows it.
	Release(ctx context.Context, id string, c *Call, charge, bundleSec int64) error
	Finish(ctx context.Context, id string, c *Call) error

	// Due claims up to n answered calls whose reserved time needs attention.
	Due(ctx context.Context, now time.Time, n int) ([]string, error)
	// Expired claims up to n calls that have outlived the longest they
	// could last with no end reported; their state is still there to read.
	Expired(ctx context.Context, now time.Time, n int) ([]string, error)
	// Forget drops a claimed expired call whose state is gone.
	Forget(ctx context.Context, id string) error
	// Extend raises the reserved time to sec, of which bundleSec billed
	// seconds in all come from the call's bundle, holds the difference and
	// schedules the next look at the call. It returns one of the ext*
	// outcomes and what is free. final marks a call that will get no more
	// time.
	Extend(ctx context.Context, id string, c *Call, sec int, bundleSec int64, final bool, f, bf Funds, next time.Time) (int, Free, error)
	// Defer moves the next look at a call; the zero time cancels it.
	Defer(ctx context.Context, id string, next time.Time) error
	// Ring records when the far end first reported progress.
	Ring(ctx context.Context, id string, now time.Time) error
	// Count returns how many calls in progress each of the given sets holds.
	Count(ctx context.Context, now time.Time, keys []string) ([]int64, error)
	// List returns calls in progress, of one client or (client 0) of all,
	// and how many there are in all.
	List(ctx context.Context, now time.Time, client int64, offset, limit int) ([]LiveCall, int64, error)
	// Purge drops the holds a snapshot loaded at syncedMs already shows, for
	// every client and bundle. Admission does the same a few at a time; this keeps an
	// idle client's funds from staying held.
	Purge(ctx context.Context, syncedMs int64) error
	// Clock is the store's time, shared by every process that settles holds.
	Clock(ctx context.Context) (time.Time, error)
}

type callRoute struct {
	ConnectionID int64  `json:"c"`
	URI          string `json:"u"`
	MaxCalls     int    `json:"mc,omitempty"`
	MaxCPS       int    `json:"mr,omitempty"`
	// A route to a client's trunk (ConnectionID is then 0) and the address
	// its registration came from, when the request must go there.
	TrunkID int64  `json:"t,omitempty"`
	NextHop string `json:"h,omitempty"`
	// Transcode: the media relay translates the call's audio on this
	// route, and the call counts among those transcoded (transcodeCalls).
	Transcode bool `json:"x,omitempty"`
	// The client a route to a trunk delivers to and its limits: the call
	// counts among that client's calls as well (ADR-0042). 0 on a route to
	// a vendor, and on routes stored before the counting began.
	DstClientID    int64 `json:"dc,omitempty"`
	ClientMaxCalls int   `json:"dmc,omitempty"`
	ClientMaxCPS   int   `json:"dmr,omitempty"`
}

// transcodeLimit is the limit argument of a route for the scripts: -1 for
// a route that is not transcoded (not counted), else the most calls that
// may be transcoded at once (0 = none).
func (r callRoute) transcodeLimit(max int) int {
	if !r.Transcode {
		return -1
	}
	return max
}

// callsKey is the set the calls on this route are counted in, cpsKey its
// counter of calls in the second sec: per vendor connection, or per trunk
// for a route that delivers to one (the same counters as the calls the
// trunk places, ADR-0042).
func (r callRoute) callsKey() string {
	if r.TrunkID != 0 {
		return trunkCalls(r.TrunkID)
	}
	return connCalls(r.ConnectionID)
}

func (r callRoute) cpsKey(sec int64) string {
	if r.TrunkID != 0 {
		return trunkCPS(r.TrunkID, sec)
	}
	return cpsKey("v", r.ConnectionID, sec)
}

// Call is what the engine remembers about a call between events.
type Call struct {
	ClientID int64       `json:"cl"`
	TrunkID  int64       `json:"tr"`
	Src      string      `json:"s"`
	SrcIP    string      `json:"ip"`
	Number   string      `json:"n"`
	TTL      int         `json:"ttl"`
	Routes   []callRoute `json:"r"`
	Rate     rating.Rate `json:"rt"` // selling rate, to price the reservation
	StartMs  int64       `json:"st"` // when the call arrived
	Country  string      `json:"co"` // destination, ISO alpha-2; "" if unknown
	Prefix   string      `json:"px"` // selling prefix
	// The bundle of minutes the call draws on and the period it counts
	// to; 0 = none.
	BundleID     int64  `json:"b,omitempty"`
	BundlePeriod string `json:"bp,omitempty"`
	// Caller is the caller's number after the rules about callers, when
	// they changed it; a connection's own rule works on it.
	Caller string `json:"ca,omitempty"`
	// MaxSec is the longest the call may last by its client's funds and its
	// trunk's limit, as decided when it arrived.
	MaxSec int `json:"mx,omitempty"`
	// TrunkCounted says the call has a place among its trunk's calls (the
	// trunk has a limit of its own).
	TrunkCounted bool `json:"tc,omitempty"`
	// Private says the caller asked for its identity to be withheld
	// (Privacy: id): no route passes its asserted identity on.
	Private bool `json:"pv,omitempty"`

	// Kept outside the JSON so they can change without rewriting it.
	Idx       int   `json:"-"` // route in use
	Leg       int   `json:"-"` // attempt number, from 1
	AttemptMs int64 `json:"-"` // when the current attempt started
	AnswerMs  int64 `json:"-"`
	RingMs    int64 `json:"-"` // first progress report of the current attempt
	Reserved  int   `json:"-"` // seconds of talk time paid for by the holds
	BundleSec int64 `json:"-"` // billed seconds of it held from the bundle
	Final     bool  `json:"-"` // no more time will be reserved
	SystemMax int   `json:"-"` // system-wide call limit to admit against; 0 = none
	// TranscodeMax is the most calls transcoded at once that a transcoded
	// route is admitted against; 0 = none may be.
	TranscodeMax int `json:"-"`
}

func newCall(in authorizeRequest, d routing.Decision, now time.Time, reserve int) *Call {
	c := &Call{ClientID: d.ClientID, TrunkID: d.TrunkID, Src: in.Src, SrcIP: in.SrcIP, Number: d.Number,
		TTL: d.MaxDuration + setupSlack, Rate: d.Sell, Leg: 1, AttemptMs: now.UnixMilli(), Reserved: reserve,
		StartMs: now.UnixMilli(), Country: numplan.Country(d.Number), Prefix: d.SellPrefix,
		BundleID: d.BundleID, BundlePeriod: d.BundlePeriod, Caller: d.Caller, MaxSec: d.MaxDuration, TrunkCounted: d.TrunkMaxCalls > 0,
		Private: privateID(in.Privacy)}
	for _, r := range d.Routes {
		c.Routes = append(c.Routes, callRoute{ConnectionID: r.ConnectionID, URI: r.URI, MaxCalls: r.MaxCalls, MaxCPS: r.MaxCPS,
			TrunkID: r.TrunkID, NextHop: r.NextHop, Transcode: r.Transcode,
			DstClientID: r.DstClientID, ClientMaxCalls: r.ClientMaxCalls, ClientMaxCPS: r.ClientMaxCPS})
	}
	return c
}

// held is the money the call's reservation takes: the cost of the reserved
// time less what the bundle pays for.
func (c *Call) held() int64 {
	return c.cost(c.Reserved, c.BundleSec)
}

// cost is what sec seconds of talk cost when the bundle pays for bundleSec
// of the seconds billed.
func (c *Call) cost(sec int, bundleSec int64) int64 {
	_, _, amount := c.Rate.AmountAfter(sec, bundleSec)
	return amount
}

// bundleFor is how much of the bundle sec seconds of talk take when free
// seconds can be had on top of what the call already holds.
func (c *Call) bundleFor(sec int, free int64) int64 {
	if c.BundleID == 0 {
		return 0
	}
	return min(int64(c.Rate.BilledSeconds(sec)), c.BundleSec+max(free, 0))
}

// reservedUntil is when the talk time paid for runs out.
func (c *Call) reservedUntil() time.Time {
	return time.UnixMilli(c.AnswerMs + int64(c.Reserved)*1000)
}

// cdr is the record for the attempt in progress, ending now.
func (c *Call) cdr(id string, now time.Time) CDR {
	rec := CDR{CallID: id, Leg: c.Leg, ClientID: c.ClientID, TrunkID: trunkRef(c.TrunkID),
		Src: c.Src, SrcIP: c.SrcIP, Dst: c.Number, StartAt: time.UnixMilli(c.AttemptMs).UTC(), EndAt: now.UTC()}
	// A leg delivered to a client's trunk went to no vendor: it names the
	// trunk instead.
	if r := c.Routes[c.Idx]; r.TrunkID == 0 {
		conn := r.ConnectionID
		rec.ConnectionID = &conn
	} else {
		dst := r.TrunkID
		rec.DstTrunkID = &dst
	}
	if c.AnswerMs != 0 {
		t := time.UnixMilli(c.AnswerMs).UTC()
		rec.AnswerAt = &t
	}
	return rec
}

// Concurrent calls are counted as members of a sorted set scored by the time
// the call must have ended, so a call whose end was never reported drops out
// by itself and every operation is safe to repeat.

// pick: ARGV[4] is the width w of a route, 3 or 5. KEYS = per candidate
// route, calls set, cps counter, transcoded calls set and, when w is 5,
// the calls set and cps counter of the client the route delivers to.
// ARGV = now, expiry, call id, w, then per route max calls, max cps, max
// transcoded and, when w is 5, the client's max calls and max cps. Max
// transcoded is -1 for a route that is not transcoded; the client's max
// calls is -1 for a route that delivers to no client (to a vendor), 0 for
// one counted without a limit. A call that is to a vendor alone is given
// w = 3: its layout has no room for clients it does not have. xfull counts
// the routes passed over because the transcoded calls were at their limit.
const pickLua = `
local xfull = 0
local function full(set, max, now)
  redis.call('ZREMRANGEBYSCORE', set, '-inf', now)
  return redis.call('ZCARD', set) >= max
end
local function over(counter, max)
  local r = redis.call('INCR', counter)
  if r == 1 then redis.call('EXPIRE', counter, 2) end
  return r > max
end
local function pick(first, now, exp, id)
  local w = tonumber(ARGV[4])
  local n = (#KEYS - first + 1) / w
  for i = 0, n - 1 do
    local k, a = first + w*i, 5 + w*i
    local calls, cps, xc = KEYS[k], KEYS[k + 1], KEYS[k + 2]
    local maxc, maxr, maxx = tonumber(ARGV[a]), tonumber(ARGV[a + 1]), tonumber(ARGV[a + 2])
    local dmaxc, dmaxr = -1, 0
    if w == 5 then dmaxc, dmaxr = tonumber(ARGV[a + 3]), tonumber(ARGV[a + 4]) end
    local ok = not (maxc > 0 and full(calls, maxc, now))
    if ok and dmaxc > 0 and full(KEYS[k + 3], dmaxc, now) then ok = false end
    if ok and maxx >= 0 and full(xc, maxx, now) then
      ok = false
      xfull = xfull + 1
    end
    if ok and maxr > 0 and over(cps, maxr) then ok = false end
    if ok and dmaxc >= 0 and dmaxr > 0 and over(KEYS[k + 4], dmaxr) then
      ok = false
      -- The route's own second was not used after all.
      if maxr > 0 then redis.call('DECR', cps) end
    end
    if ok then
      redis.call('ZADD', calls, exp, id)
      if dmaxc >= 0 then redis.call('ZADD', KEYS[k + 3], exp, id) end
      if maxx >= 0 then redis.call('ZADD', xc, exp, id) end
      return i
    end
  end
  return -1
end
`

// Money held per client: a hash of call id -> amount, their sum, and a
// sorted set scored by the time (store clock, ms) after which a hold no
// longer counts. A call in progress is scored with its expiry; a finished
// one with the time the rater settled its charge, or far ahead until then.
// Holds scored at or before the snapshot's load time are dropped: the
// balance in that snapshot already shows them.
//
// A bundle of minutes is kept the same way, per bundle and period, with
// seconds for amounts: what the snapshot saw left of it takes the place of
// the balance. A call that draws on no bundle passes the keys of a ledger
// nothing is ever written to.
//
// Amounts are whole micro-units; Lua keeps them exact up to 2^53.
const fundsLua = `
local function nowms()
  local t = redis.call('TIME')
  return t[1] * 1000 + math.floor(t[2] / 1000)
end
local function available(z, h, s, funds, synced)
  local ids = redis.call('ZRANGEBYSCORE', z, '-inf', synced, 'LIMIT', 0, 200)
  for _, id in ipairs(ids) do
    local a = redis.call('HGET', h, id)
    if a then
      redis.call('DECRBY', s, a)
      redis.call('HDEL', h, id)
    end
    redis.call('ZREM', z, id)
  end
  return funds - tonumber(redis.call('GET', s) or 0)
end
`

// admit: KEYS = call, client calls set, client cps counter, holds set, held
// hash, held sum, holders set, all calls set, country calls set, the
// bundle's holds set, held hash and held sum, the trunk's calls set and cps
// counter (the same ones a call delivered to the trunk counts in), the
// expiry set, then routes (see pick). ARGV = now, expiry, call id, route
// width, route limits..., then
// the trunk's max calls and max cps, the bundle's ledger name, seconds
// the snapshot saw left of the bundle, seconds of it to hold, client id,
// client max calls, client max cps, state JSON, attempt ms, ttl seconds,
// funds, amount to hold, snapshot time, reserved seconds, the most calls the
// whole system may carry (0 = no limit).
// Returns {route index or limit, money free before this call, bundle
// seconds free before it, routes passed over for the transcoding limit}.
var admitScript = redis.NewScript(pickLua + fundsLua + `
local now, exp, id = tonumber(ARGV[1]), tonumber(ARGV[2]), ARGV[3]
local n = #ARGV
local maxc, maxr, state, at, ttl = tonumber(ARGV[n-9]), tonumber(ARGV[n-8]), ARGV[n-7], ARGV[n-6], tonumber(ARGV[n-5])
local funds, need, synced, rsec = tonumber(ARGV[n-4]), tonumber(ARGV[n-3]), ARGV[n-2], ARGV[n-1]
local bfunds, bneed = tonumber(ARGV[n-12]), tonumber(ARGV[n-11])
if redis.call('EXISTS', KEYS[1]) == 1 then
  local own = tonumber(redis.call('HGET', KEYS[5], id) or 0)
  local bown = tonumber(redis.call('HGET', KEYS[11], id) or 0)
  return {tonumber(redis.call('HGET', KEYS[1], 'i')), funds - tonumber(redis.call('GET', KEYS[6]) or 0) + own,
    bfunds - tonumber(redis.call('GET', KEYS[12]) or 0) + bown}
end
local sysmax = tonumber(ARGV[n])
if sysmax > 0 then
  redis.call('ZREMRANGEBYSCORE', KEYS[8], '-inf', now)
  if redis.call('ZCARD', KEYS[8]) >= sysmax then return {-5, 0, 0} end
end
local bavail = 0
if bneed > 0 then
  bavail = available(KEYS[10], KEYS[11], KEYS[12], bfunds, synced)
  if bavail < bneed then return {-6, 0, bavail} end
end
local avail = available(KEYS[4], KEYS[5], KEYS[6], funds, synced)
if avail < need then return {-4, avail, bavail} end
if maxc > 0 then
  redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
  if redis.call('ZCARD', KEYS[2]) >= maxc then return {-1, avail, bavail} end
end
if maxr > 0 then
  local r = redis.call('INCR', KEYS[3])
  if r == 1 then redis.call('EXPIRE', KEYS[3], 2) end
  if r > maxr then return {-2, avail, bavail} end
end
local tmaxc, tmaxr = tonumber(ARGV[n-15]), tonumber(ARGV[n-14])
if tmaxc > 0 then
  redis.call('ZREMRANGEBYSCORE', KEYS[13], '-inf', now)
  if redis.call('ZCARD', KEYS[13]) >= tmaxc then return {-7, avail, bavail} end
end
if tmaxr > 0 then
  local r = redis.call('INCR', KEYS[14])
  if r == 1 then redis.call('EXPIRE', KEYS[14], 2) end
  if r > tmaxr then return {-8, avail, bavail} end
end
local i = pick(16, now, exp, id)
if i < 0 then return {-3, avail, bavail, xfull} end
redis.call('ZADD', KEYS[2], exp, id)
if tmaxc > 0 then redis.call('ZADD', KEYS[13], exp, id) end
redis.call('ZADD', KEYS[8], exp, id)
redis.call('ZADD', KEYS[9], exp, id)
redis.call('ZADD', KEYS[15], exp, id)
redis.call('HSET', KEYS[1], 's', state, 'i', i, 'l', 1, 't', at, 'r', rsec, 'b', bneed)
-- The state outlives the call so that its record can be written.
redis.call('EXPIRE', KEYS[1], ttl + ` + strconv.Itoa(int(expiryGrace/time.Second)) + `)
redis.call('HSET', KEYS[5], id, ARGV[n-3])
redis.call('INCRBY', KEYS[6], ARGV[n-3])
redis.call('ZADD', KEYS[4], nowms() + ttl * 1000, id)
redis.call('SADD', KEYS[7], ARGV[n-10])
if bneed > 0 then
  redis.call('HSET', KEYS[11], id, bneed)
  redis.call('INCRBY', KEYS[12], bneed)
  redis.call('ZADD', KEYS[10], nowms() + ttl * 1000, id)
  redis.call('SADD', KEYS[7], ARGV[n-13])
end
return {i, avail, bavail, xfull}
`)

// next: KEYS = call, calls set of the route being left, the transcoded
// calls set, calls set of the client the route being left delivered to,
// then the remaining routes (see pick). ARGV = now, expiry, call id, route
// width, route limits..., then whether to leave the route's calls set
// (0/1) and the client's (0/1), attempt ms. A set is kept when the call
// counts in it as the caller's as well: the trunk it came from, its
// client. Returns {the offset of the admitted route among the remaining
// ones or -1, routes passed over for the transcoding limit}.
var nextScript = redis.NewScript(pickLua + `
local now, exp, id = tonumber(ARGV[1]), tonumber(ARGV[2]), ARGV[3]
if redis.call('EXISTS', KEYS[1]) == 0 then return {-1, 0} end
if ARGV[#ARGV - 2] == '1' then redis.call('ZREM', KEYS[2], id) end
redis.call('ZREM', KEYS[3], id)
if ARGV[#ARGV - 1] == '1' then redis.call('ZREM', KEYS[4], id) end
local i = pick(5, now, exp, id)
if i < 0 then return {-1, xfull} end
redis.call('HINCRBY', KEYS[1], 'i', i + 1)
redis.call('HINCRBY', KEYS[1], 'l', 1)
redis.call('HSET', KEYS[1], 't', ARGV[#ARGV])
redis.call('HDEL', KEYS[1], 'g')
return {i, xfull}
`)

// answer: KEYS = call, due set. ARGV = answer ms, lead ms, call id.
var answerScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 and redis.call('HSETNX', KEYS[1], 'a', ARGV[1]) == 1 then
  local r = tonumber(redis.call('HGET', KEYS[1], 'r') or 0)
  redis.call('ZADD', KEYS[2], ARGV[1] + r * 1000 - ARGV[2], ARGV[3])
end
return 0
`)

// ring: KEYS = call. ARGV = ms.
var ringScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then redis.call('HSETNX', KEYS[1], 'g', ARGV[1]) end
return 0
`)

// release: KEYS = call, holds set, held hash, held sum, due set, then the
// bundle's holds set, held hash and held sum.
// ARGV = call id, charge, ms to keep an unsettled charge held, seconds
// taken from the bundle.
var releaseScript = redis.NewScript(fundsLua + `
if redis.call('EXISTS', KEYS[1]) == 0 or redis.call('HSETNX', KEYS[1], 'p', 1) == 0 then return 0 end
local id = ARGV[1]
redis.call('ZREM', KEYS[5], id)
local function settle(z, h, s, charge)
  local old = tonumber(redis.call('HGET', h, id) or 0)
  if charge <= 0 then
    if redis.call('HDEL', h, id) == 1 then redis.call('DECRBY', s, old) end
    redis.call('ZREM', z, id)
  else
    redis.call('HSET', h, id, charge)
    redis.call('INCRBY', s, charge - old)
    redis.call('ZADD', z, nowms() + ARGV[3], id)
  end
end
settle(KEYS[2], KEYS[3], KEYS[4], tonumber(ARGV[2]))
settle(KEYS[6], KEYS[7], KEYS[8], tonumber(ARGV[4]))
return 1
`)

// extend: KEYS = call, holds set, held hash, held sum, due set, then the
// bundle's holds set, held hash and held sum, and the holders set.
// ARGV = call id, reserved seconds now, reserved seconds wanted, amount to
// add (it may be less than nothing when the bundle takes over seconds the
// balance was held for), funds, snapshot time, final (0/1), next look ms,
// bundle seconds to add, bundle seconds the snapshot saw left, the bundle's
// ledger name.
// Returns {1 done | 0 not enough money | 3 not enough in the bundle |
// 2 changed meanwhile | -1 gone, money free, bundle seconds free}.
var extendScript = redis.NewScript(fundsLua + `
if redis.call('EXISTS', KEYS[1]) == 0 or redis.call('HEXISTS', KEYS[1], 'p') == 1 then return {-1, 0, 0} end
if redis.call('HGET', KEYS[1], 'r') ~= ARGV[2] then return {2, 0, 0} end
local id, delta, avail = ARGV[1], tonumber(ARGV[4]), 0
local bdelta, bavail = tonumber(ARGV[9]), 0
if bdelta > 0 then
  bavail = available(KEYS[6], KEYS[7], KEYS[8], tonumber(ARGV[10]), ARGV[6])
  if bavail < bdelta then return {3, 0, bavail} end
end
if delta > 0 then
  avail = available(KEYS[2], KEYS[3], KEYS[4], tonumber(ARGV[5]), ARGV[6])
  if avail < delta then return {0, avail, bavail} end
end
if delta ~= 0 then
  redis.call('HINCRBY', KEYS[3], id, ARGV[4])
  redis.call('INCRBY', KEYS[4], ARGV[4])
end
if bdelta > 0 then
  redis.call('HINCRBY', KEYS[7], id, ARGV[9])
  redis.call('INCRBY', KEYS[8], ARGV[9])
  redis.call('HINCRBY', KEYS[1], 'b', ARGV[9])
  -- A call that got none of its bundle at first has no place in it yet.
  if not redis.call('ZSCORE', KEYS[6], id) then
    redis.call('ZADD', KEYS[6], redis.call('ZSCORE', KEYS[2], id) or (nowms() + 86400000), id)
    redis.call('SADD', KEYS[9], ARGV[11])
  end
end
redis.call('HSET', KEYS[1], 'r', ARGV[3])
if ARGV[7] == '1' then redis.call('HSET', KEYS[1], 'f', 1) end
redis.call('ZADD', KEYS[5], ARGV[8], id)
return {1, avail, bavail}
`)

// due: KEYS = due set. ARGV = now ms, lease ms, count. A claimed call is
// pushed back by the lease so no other engine picks it up meanwhile, and so
// it comes back if this one dies before dealing with it.
var dueScript = redis.NewScript(`
local ids = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1], 'LIMIT', 0, ARGV[3])
for _, id in ipairs(ids) do redis.call('ZADD', KEYS[1], ARGV[1] + ARGV[2], id) end
return ids
`)

// settle: KEYS = holds set. ARGV = call ids. Stamps the holds of finished
// calls with the time their charge reached the balance, or their seconds
// the bundle's count.
var settleScript = redis.NewScript(fundsLua + `
local t = nowms()
for _, id in ipairs(ARGV) do
  if redis.call('ZSCORE', KEYS[1], id) then redis.call('ZADD', KEYS[1], t, id) end
end
return 0
`)

// purge: KEYS = holds set, held hash, held sum, holders set.
// ARGV = snapshot time, ledger name. Returns 1 while there is more to drop.
var purgeScript = redis.NewScript(fundsLua + `
available(KEYS[1], KEYS[2], KEYS[3], 0, ARGV[1])
if redis.call('ZCARD', KEYS[1]) == 0 then
  redis.call('SREM', KEYS[4], ARGV[2])
  redis.call('DEL', KEYS[3])
  return 0
end
return redis.call('ZCOUNT', KEYS[1], '-inf', ARGV[1]) > 0 and 1 or 0
`)

const dueKey = "due"

// expiryKey is the set of calls in progress scored by the moment (unix
// seconds) they can no longer be: past it, a call whose end was never
// reported gets its record from the engine.
const expiryKey = "expiry"

// expiredScript: KEYS = expiry set. ARGV = now (s), lease (s), count. Like
// due: a claimed call comes back after the lease if nothing finished it.
var expiredScript = redis.NewScript(`
local ids = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1], 'LIMIT', 0, ARGV[3])
for _, id in ipairs(ids) do redis.call('ZADD', KEYS[1], ARGV[1] + ARGV[2], id) end
return ids
`)

// holdersKey is the set of ledgers that hold something: the clients with
// money held (by id) and the bundles with seconds held (by ledger name).
const holdersKey = "holders"

// dueLease is how long a claimed call is left alone.
const dueLease = 5 * time.Second

// RedisCalls is the Calls implementation used in production.
type RedisCalls struct{ rdb redis.UniversalClient }

func NewRedisCalls(rdb redis.UniversalClient) *RedisCalls { return &RedisCalls{rdb} }

// allCalls is the set of every call in progress, scored like the per-client
// sets.
const allCalls = "cc:all"

// transcodeCalls is the set of calls in progress whose audio the media
// relay translates, scored like the others: the engine's limit of them
// (TRANSCODE_MAX) is counted here.
const transcodeCalls = "cc:x"

func countryCalls(iso string) string { return "cc:n:" + iso }

func callKey(id string) string    { return "call:" + id }
func clientCalls(id int64) string { return "cc:c:" + strconv.FormatInt(id, 10) }
func connCalls(id int64) string   { return "cc:v:" + strconv.FormatInt(id, 10) }

// trunkCalls is the set of calls in progress of a trunk, in both
// directions (ADR-0042): those it placed, when it has a limit of its own,
// and every call delivered to it. trunkCPS counts both in a second.
func trunkCalls(id int64) string    { return "cc:t:" + strconv.FormatInt(id, 10) }
func trunkCPS(id, sec int64) string { return cpsKey("t", id, sec) }

// legacyTrunkCalls is where the calls a trunk placed were counted before
// ADR-0042; MigrateTrunkCounts folds what is left there into trunkCalls.
const legacyTrunkCalls = "cc:s:"

func cpsKey(kind string, id int64, sec int64) string {
	return "cps:" + kind + ":" + strconv.FormatInt(id, 10) + ":" + strconv.FormatInt(sec, 10)
}

// holdKeys are the holds set, the held hash and the held sum of a client.
func holdKeys(client int64) []string {
	return ledgerKeys(strconv.FormatInt(client, 10))
}

func ledgerKeys(name string) []string {
	return []string{"hold:" + name, "held:" + name, "heldsum:" + name}
}

// BundleLedger names the ledger of a bundle's seconds in one period.
func BundleLedger(bundle int64, period string) string {
	return "b" + strconv.FormatInt(bundle, 10) + ":" + period
}

// noLedger stands in for the bundle of a call that has none.
const noLedger = "b0"

// bundleLedger is the ledger the call's bundle seconds are held in.
func (c *Call) bundleLedger() string {
	if c.BundleID == 0 {
		return noLedger
	}
	return BundleLedger(c.BundleID, c.BundlePeriod)
}

// routeArgs are the route width (see pick) and the keys and limits of the
// candidate routes of call c. left is the route the call is leaving, nil
// at admission.
//
// A call counts once in each set and once in each second, whichever way
// it reaches it (ADR-0042):
//   - a route to the trunk or the client the call is leaving does not count
//     again towards their calls a second: moving to another registration of
//     the same trunk is still the same call to it;
//   - a route to the trunk the call came from, or to a trunk of the call's
//     own client, is not checked again: the admission has checked the
//     caller's side already, and the call keeps its place there.
func routeArgs(c *Call, routes []callRoute, sec int64, left *callRoute) (width int, keys []string, limits []any) {
	width = 3
	for _, r := range routes {
		if r.DstClientID != 0 {
			width = 5
		}
	}
	for _, r := range routes {
		keys = append(keys, r.callsKey(), r.cpsKey(sec), transcodeCalls)
		calls, cps := r.MaxCalls, r.MaxCPS
		if r.TrunkID != 0 && r.TrunkID == c.TrunkID {
			cps = 0
			if c.TrunkCounted {
				calls = 0
			}
		}
		if left != nil && left.TrunkID != 0 && r.TrunkID == left.TrunkID {
			cps = 0
		}
		limits = append(limits, calls, cps, r.transcodeLimit(c.TranscodeMax))
		if width == 3 {
			continue
		}
		keys = append(keys, clientCalls(r.DstClientID), cpsKey("c", r.DstClientID, sec))
		ccalls, ccps := r.ClientMaxCalls, r.ClientMaxCPS
		switch {
		case r.DstClientID == 0:
			ccalls, ccps = -1, 0
		case r.DstClientID == c.ClientID:
			ccalls, ccps = 0, 0
		case left != nil && left.DstClientID == r.DstClientID:
			ccps = 0
		}
		limits = append(limits, ccalls, ccps)
	}
	return
}

// leaves says whether a call that moves on from route r leaves r's calls
// set and the set of r's client: not when it counts there as the caller's.
func (c *Call) leaves(r callRoute) (route, client int) {
	if !(r.TrunkID != 0 && r.TrunkID == c.TrunkID && c.TrunkCounted) {
		route = 1
	}
	if r.DstClientID != 0 && r.DstClientID != c.ClientID {
		client = 1
	}
	return
}

func triple(res []int64, err error) (int64, Free, error) {
	if err != nil || len(res) < 3 {
		return 0, Free{}, err
	}
	free := Free{Money: res[1], Bundle: res[2]}
	if len(res) > 3 {
		free.TranscodeFull = res[3]
	}
	return res[0], free, nil
}

func (s *RedisCalls) Admit(ctx context.Context, id string, c *Call, d routing.Decision, f, bf Funds, now time.Time) (int, Free, error) {
	state, err := json.Marshal(c)
	if err != nil {
		return 0, Free{}, err
	}
	sec := now.Unix()
	width, rkeys, limits := routeArgs(c, c.Routes, sec, nil)
	keys := append([]string{callKey(id), clientCalls(c.ClientID), cpsKey("c", c.ClientID, sec)}, holdKeys(c.ClientID)...)
	keys = append(append(keys, holdersKey, allCalls, countryCalls(c.Country)), ledgerKeys(c.bundleLedger())...)
	keys = append(keys, trunkCalls(c.TrunkID), trunkCPS(c.TrunkID, sec), expiryKey)
	keys = append(keys, rkeys...)
	args := append([]any{sec, sec + int64(c.TTL), id, width}, limits...)
	args = append(args, d.TrunkMaxCalls, d.TrunkMaxCPS, c.bundleLedger(), bf.Total, c.BundleSec,
		c.ClientID, d.MaxCalls, d.MaxCPS, state, c.AttemptMs, c.TTL, f.Total, c.held(), f.SyncedMs, c.Reserved, c.SystemMax)
	idx, free, err := triple(admitScript.Run(ctx, s.rdb, keys, args...).Int64Slice())
	return int(idx), free, err
}

func (s *RedisCalls) Next(ctx context.Context, id string, c *Call, now time.Time) (int, int64, error) {
	rest := c.Routes[c.Idx+1:]
	if len(rest) == 0 {
		return -1, 0, nil
	}
	sec := now.Unix()
	left := c.Routes[c.Idx]
	width, rkeys, limits := routeArgs(c, rest, sec, &left)
	keys := append([]string{callKey(id), left.callsKey(), transcodeCalls, clientCalls(left.DstClientID)}, rkeys...)
	args := append([]any{sec, sec + int64(c.TTL), id, width}, limits...)
	route, client := c.leaves(left)
	args = append(args, route, client, now.UnixMilli())
	res, err := nextScript.Run(ctx, s.rdb, keys, args...).Int64Slice()
	if err != nil || len(res) != 2 {
		return -1, 0, err
	}
	if res[0] < 0 {
		return -1, res[1], nil
	}
	return c.Idx + 1 + int(res[0]), res[1], nil
}

func (s *RedisCalls) Answer(ctx context.Context, id string, now time.Time, lead time.Duration) error {
	return answerScript.Run(ctx, s.rdb, []string{callKey(id), dueKey}, now.UnixMilli(), lead.Milliseconds(), id).Err()
}

func (s *RedisCalls) Get(ctx context.Context, id string) (*Call, error) {
	h, err := s.rdb.HGetAll(ctx, callKey(id)).Result()
	if err != nil || len(h) == 0 {
		return nil, err
	}
	c := &Call{}
	if err := json.Unmarshal([]byte(h["s"]), c); err != nil {
		return nil, err
	}
	c.Idx, _ = strconv.Atoi(h["i"])
	c.Leg, _ = strconv.Atoi(h["l"])
	c.AttemptMs, _ = strconv.ParseInt(h["t"], 10, 64)
	c.AnswerMs, _ = strconv.ParseInt(h["a"], 10, 64)
	c.RingMs, _ = strconv.ParseInt(h["g"], 10, 64)
	c.Reserved, _ = strconv.Atoi(h["r"])
	c.BundleSec, _ = strconv.ParseInt(h["b"], 10, 64)
	c.Final = h["f"] != ""
	if c.Idx >= len(c.Routes) {
		c.Idx = len(c.Routes) - 1
	}
	return c, nil
}

func (s *RedisCalls) Release(ctx context.Context, id string, c *Call, charge, bundleSec int64) error {
	keys := append(append([]string{callKey(id)}, holdKeys(c.ClientID)...), dueKey)
	keys = append(keys, ledgerKeys(c.bundleLedger())...)
	return releaseScript.Run(ctx, s.rdb, keys, id, charge, settleWait.Milliseconds(), bundleSec).Err()
}

func (s *RedisCalls) Finish(ctx context.Context, id string, c *Call) error {
	_, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		p.ZRem(ctx, clientCalls(c.ClientID), id)
		p.ZRem(ctx, c.Routes[c.Idx].callsKey(), id)
		if dst := c.Routes[c.Idx].DstClientID; dst != 0 && dst != c.ClientID {
			p.ZRem(ctx, clientCalls(dst), id)
		}
		p.ZRem(ctx, dueKey, id)
		p.ZRem(ctx, expiryKey, id)
		p.ZRem(ctx, allCalls, id)
		p.ZRem(ctx, countryCalls(c.Country), id)
		if c.TrunkCounted {
			p.ZRem(ctx, trunkCalls(c.TrunkID), id)
		}
		if c.Routes[c.Idx].Transcode {
			p.ZRem(ctx, transcodeCalls, id)
		}
		p.Del(ctx, callKey(id))
		return nil
	})
	return err
}

func (s *RedisCalls) Due(ctx context.Context, now time.Time, n int) ([]string, error) {
	return dueScript.Run(ctx, s.rdb, []string{dueKey}, now.UnixMilli(), dueLease.Milliseconds(), n).StringSlice()
}

func (s *RedisCalls) Expired(ctx context.Context, now time.Time, n int) ([]string, error) {
	return expiredScript.Run(ctx, s.rdb, []string{expiryKey}, now.Unix(), int64(expiryLease/time.Second), n).StringSlice()
}

func (s *RedisCalls) Forget(ctx context.Context, id string) error {
	return s.rdb.ZRem(ctx, expiryKey, id).Err()
}

func (s *RedisCalls) Extend(ctx context.Context, id string, c *Call, sec int, bundleSec int64, final bool, f, bf Funds, next time.Time) (int, Free, error) {
	fin := 0
	if final {
		fin = 1
	}
	keys := append(append([]string{callKey(id)}, holdKeys(c.ClientID)...), dueKey)
	keys = append(append(keys, ledgerKeys(c.bundleLedger())...), holdersKey)
	res, free, err := triple(extendScript.Run(ctx, s.rdb, keys,
		id, c.Reserved, sec, c.cost(sec, bundleSec)-c.held(), f.Total, f.SyncedMs, fin, next.UnixMilli(),
		max(bundleSec-c.BundleSec, 0), bf.Total, c.bundleLedger()).Int64Slice())
	if err != nil {
		return extGone, free, err
	}
	return int(res), free, nil
}

func (s *RedisCalls) Defer(ctx context.Context, id string, next time.Time) error {
	if next.IsZero() {
		return s.rdb.ZRem(ctx, dueKey, id).Err()
	}
	return s.rdb.ZAddXX(ctx, dueKey, redis.Z{Score: float64(next.UnixMilli()), Member: id}).Err()
}

func (s *RedisCalls) Ring(ctx context.Context, id string, now time.Time) error {
	return ringScript.Run(ctx, s.rdb, []string{callKey(id)}, now.UnixMilli()).Err()
}

func (s *RedisCalls) Count(ctx context.Context, now time.Time, keys []string) ([]int64, error) {
	min := "(" + strconv.FormatInt(now.Unix(), 10)
	cmds, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, k := range keys {
			p.ZCount(ctx, k, min, "+inf")
		}
		return nil
	})
	out := make([]int64, len(cmds))
	for i, c := range cmds {
		out[i] = c.(*redis.IntCmd).Val()
	}
	return out, err
}

// LiveCall is a call in progress as the live list shows it.
type LiveCall struct {
	CallID       string     `json:"call_id"`
	ClientID     int64      `json:"client_id"`
	ConnectionID int64      `json:"connection_id"`
	Src          string     `json:"src"`
	SrcIP        string     `json:"src_ip"`
	Dst          string     `json:"dst"`
	Country      string     `json:"country"`
	Leg          int        `json:"leg"`
	StartAt      time.Time  `json:"start_at"`
	RingAt       *time.Time `json:"ring_at"`
	AnswerAt     *time.Time `json:"answer_at"`
}

func (s *RedisCalls) List(ctx context.Context, now time.Time, client int64, offset, limit int) ([]LiveCall, int64, error) {
	key := allCalls
	if client != 0 {
		key = clientCalls(client)
	}
	by := &redis.ZRangeBy{Min: "(" + strconv.FormatInt(now.Unix(), 10), Max: "+inf", Offset: int64(offset), Count: int64(limit)}
	total, err := s.rdb.ZCount(ctx, key, by.Min, by.Max).Result()
	if err != nil {
		return nil, 0, err
	}
	ids, err := s.rdb.ZRangeByScore(ctx, key, by).Result()
	if err != nil {
		return nil, 0, err
	}
	out := make([]LiveCall, 0, len(ids))
	for _, id := range ids { // a page is small; one read per call keeps this simple
		c, err := s.Get(ctx, id)
		if err != nil {
			return nil, 0, err
		}
		if c == nil {
			continue // ended meanwhile
		}
		lc := LiveCall{CallID: id, ClientID: c.ClientID, ConnectionID: c.Routes[c.Idx].ConnectionID, Src: c.Src,
			SrcIP: c.SrcIP, Dst: c.Number, Country: c.Country, Leg: c.Leg, StartAt: time.UnixMilli(c.StartMs).UTC()}
		if c.RingMs != 0 {
			t := time.UnixMilli(c.RingMs).UTC()
			lc.RingAt = &t
		}
		if c.AnswerMs != 0 {
			t := time.UnixMilli(c.AnswerMs).UTC()
			lc.AnswerAt = &t
		}
		out = append(out, lc)
	}
	return out, total, nil
}

func (s *RedisCalls) Purge(ctx context.Context, syncedMs int64) error {
	clients, err := s.rdb.SMembers(ctx, holdersKey).Result()
	for len(clients) > 0 && err == nil {
		var cmds []redis.Cmder
		cmds, err = s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
			for _, cl := range clients {
				purgeScript.Eval(ctx, p, append(ledgerKeys(cl), holdersKey), syncedMs, cl)
			}
			return nil
		})
		var more []string
		for i, cmd := range cmds {
			if n, _ := cmd.(*redis.Cmd).Int(); n == 1 {
				more = append(more, clients[i])
			}
		}
		clients = more
	}
	return err
}

// migrateScript folds the set a trunk's calls were counted in before
// ADR-0042 into its one set: KEYS = old set, new set. A member in both
// keeps the later expiry.
var migrateScript = redis.NewScript(`
redis.call('ZUNIONSTORE', KEYS[2], 2, KEYS[2], KEYS[1], 'AGGREGATE', 'MAX')
redis.call('DEL', KEYS[1])
return 0
`)

// MigrateTrunkCounts moves the calls trunks placed, counted under the key
// used before ADR-0042 (cc:s:<trunk>), into the set the trunk's limit now
// counts both directions in (cc:t:<trunk>), and says for how many trunks.
// The engine runs it when it starts; it is safe to repeat. The counters of
// calls a second need nothing: they last two seconds.
func MigrateTrunkCounts(ctx context.Context, rdb redis.UniversalClient) (int, error) {
	n := 0
	it := rdb.Scan(ctx, 0, legacyTrunkCalls+"*", 500).Iterator()
	for it.Next(ctx) {
		old := it.Val()
		trunk := strings.TrimPrefix(old, legacyTrunkCalls)
		if err := migrateScript.Run(ctx, rdb, []string{old, "cc:t:" + trunk}).Err(); err != nil {
			return n, fmt.Errorf("trunk %s: %w", trunk, err)
		}
		n++
	}
	if err := it.Err(); err != nil {
		return n, fmt.Errorf("listing old trunk counts: %w", err)
	}
	return n, nil
}

func (s *RedisCalls) Clock(ctx context.Context) (time.Time, error) {
	return s.rdb.Time(ctx).Result()
}

// Settler tells the engine that the charges of finished calls have reached
// the balance. The rater calls it after its transaction commits.
type Settler struct{ rdb redis.UniversalClient }

func NewSettler(rdb redis.UniversalClient) *Settler { return &Settler{rdb} }

// Settled marks the calls of each client. It is safe to repeat.
func (s *Settler) Settled(ctx context.Context, byClient map[int64][]string) error {
	byLedger := make(map[string][]string, len(byClient))
	for client, ids := range byClient {
		byLedger[strconv.FormatInt(client, 10)] = ids
	}
	return s.settled(ctx, byLedger)
}

// BundlesSettled marks the calls whose seconds have been counted against
// each bundle ledger (see BundleLedger). It is safe to repeat.
func (s *Settler) BundlesSettled(ctx context.Context, byLedger map[string][]string) error {
	return s.settled(ctx, byLedger)
}

func (s *Settler) settled(ctx context.Context, byLedger map[string][]string) error {
	_, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		for ledger, ids := range byLedger {
			args := make([]any, len(ids))
			for i, id := range ids {
				args[i] = id
			}
			settleScript.Eval(ctx, p, ledgerKeys(ledger)[:1], args...)
		}
		return nil
	})
	return err
}
