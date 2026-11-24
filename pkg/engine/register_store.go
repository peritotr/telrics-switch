// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Bindings live in the state store, next to the calls: a hash per trunk,
// Contact -> binding, that expires with its last binding, and a set of the
// trunks that have one. Every engine that shares the store sees them, and
// they outlive a restart of the engine or of the SIP core.

// storedBinding is a binding as kept: with its end and the moment it was
// last renewed in milliseconds, for the store's scripts to compare.
type storedBinding struct {
	Binding
	UntilMs   int64 `json:"u"`
	RenewedMs int64 `json:"a"`
}

const regIndex = "regs"

func regKey(trunk int64) string { return "reg:" + strconv.FormatInt(trunk, 10) }

// regTidy: KEYS = the trunk's hash, the index. ARGV = now ms, most
// bindings to keep, trunk id, then optionally a Contact and its binding to
// store first. Drops what has expired, then the bindings renewed longest
// ago beyond the limit, and returns the rest.
var regTidyScript = redis.NewScript(`
local now, keep = tonumber(ARGV[1]), tonumber(ARGV[2])
if ARGV[4] then redis.call('HSET', KEYS[1], ARGV[4], ARGV[5]) end
local all = redis.call('HGETALL', KEYS[1])
local live, last = {}, 0
for i = 1, #all, 2 do
  local ok, b = pcall(cjson.decode, all[i + 1])
  if ok and type(b) == 'table' and tonumber(b.u) and tonumber(b.u) > now then
    live[#live + 1] = {f = all[i], v = all[i + 1], a = tonumber(b.a) or 0, u = tonumber(b.u)}
  else
    redis.call('HDEL', KEYS[1], all[i])
  end
end
table.sort(live, function(x, y) if x.a ~= y.a then return x.a > y.a end return x.f < y.f end)
local out = {}
for i, b in ipairs(live) do
  if i > keep then
    redis.call('HDEL', KEYS[1], b.f)
  else
    out[#out + 1] = b.v
    if b.u > last then last = b.u end
  end
end
if #out == 0 then
  redis.call('DEL', KEYS[1])
  redis.call('SREM', KEYS[2], ARGV[3])
else
  redis.call('PEXPIREAT', KEYS[1], last)
  redis.call('SADD', KEYS[2], ARGV[3])
end
return out
`)

// RedisRegistry is the Registry used in production.
type RedisRegistry struct{ rdb redis.UniversalClient }

func NewRedisRegistry(rdb redis.UniversalClient) *RedisRegistry { return &RedisRegistry{rdb} }

// noLimit is passed where nothing is to make way.
const noLimit = 1 << 20

func (s *RedisRegistry) tidy(ctx context.Context, trunk int64, keep int, now time.Time, put ...any) ([]Binding, error) {
	args := append([]any{now.UnixMilli(), keep, trunk}, put...)
	raw, err := regTidyScript.Run(ctx, s.rdb, []string{regKey(trunk), regIndex}, args...).StringSlice()
	if err != nil {
		return nil, err
	}
	return decodeBindings(raw, now), nil
}

// decodeBindings reads stored bindings, leaves out the expired and puts
// the most recently renewed first.
func decodeBindings(raw []string, now time.Time) []Binding {
	list := make([]storedBinding, 0, len(raw))
	for _, v := range raw {
		var b storedBinding
		if json.Unmarshal([]byte(v), &b) == nil && b.UntilMs > now.UnixMilli() {
			list = append(list, b)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].RenewedMs != list[j].RenewedMs {
			return list[i].RenewedMs > list[j].RenewedMs
		}
		return list[i].Contact < list[j].Contact
	})
	out := make([]Binding, len(list))
	for i := range list {
		out[i] = list[i].Binding
	}
	return out
}

func (s *RedisRegistry) Put(ctx context.Context, b Binding, max int, now time.Time) ([]Binding, error) {
	// A binding that is renewed keeps the moment it first came. A request
	// older than the one it was last renewed by (same Call-ID, lower CSeq)
	// arrived out of order and changes nothing.
	if old, err := s.rdb.HGet(ctx, regKey(b.TrunkID), b.Contact).Result(); err == nil {
		var was storedBinding
		if json.Unmarshal([]byte(old), &was) == nil && was.UntilMs > now.UnixMilli() {
			if was.CallID != "" && was.CallID == b.CallID && b.CSeq < was.CSeq {
				return s.Of(ctx, b.TrunkID, now)
			}
			b.Since = was.Since
		}
	} else if err != redis.Nil {
		return nil, err
	}
	v, err := json.Marshal(storedBinding{Binding: b, UntilMs: b.Until.UnixMilli(), RenewedMs: now.UnixMilli()})
	if err != nil {
		return nil, err
	}
	return s.tidy(ctx, b.TrunkID, max, now, b.Contact, string(v))
}

func (s *RedisRegistry) Remove(ctx context.Context, trunk int64, contact string, now time.Time) ([]Binding, error) {
	var err error
	if contact == "" {
		err = s.rdb.Del(ctx, regKey(trunk)).Err()
	} else {
		err = s.rdb.HDel(ctx, regKey(trunk), contact).Err()
	}
	if err != nil {
		return nil, err
	}
	return s.tidy(ctx, trunk, noLimit, now)
}

func (s *RedisRegistry) Of(ctx context.Context, trunk int64, now time.Time) ([]Binding, error) {
	raw, err := s.rdb.HVals(ctx, regKey(trunk)).Result()
	if err != nil {
		return nil, err
	}
	return decodeBindings(raw, now), nil
}

func (s *RedisRegistry) All(ctx context.Context, now time.Time) ([]Binding, error) {
	trunks, err := s.rdb.SMembers(ctx, regIndex).Result()
	if err != nil || len(trunks) == 0 {
		return nil, err
	}
	cmds, err := s.rdb.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, t := range trunks {
			p.HVals(ctx, "reg:"+t)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []Binding
	var gone []any
	for i, cmd := range cmds {
		list := decodeBindings(cmd.(*redis.StringSliceCmd).Val(), now)
		if len(list) == 0 {
			gone = append(gone, trunks[i]) // expired by itself: forget the trunk
		}
		out = append(out, list...)
	}
	if len(gone) > 0 {
		s.rdb.SRem(ctx, regIndex, gone...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TrunkID < out[j].TrunkID })
	return out, nil
}
