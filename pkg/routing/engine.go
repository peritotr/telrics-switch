// SPDX-License-Identifier: AGPL-3.0-only

// Package routing makes the per-call decision from an in-memory snapshot.
// Nothing in Authorize touches a database: that is the call-path rule.
package routing

import (
	"net/netip"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"softswitch/core/pkg/rating"
)

// MaxCallSeconds caps every call regardless of balance.
const MaxCallSeconds = 7200

// Trunk is how a client's calls are recognised: by source address, or by a
// user name whose password the SIP core has verified.
type Trunk struct {
	ID, ClientID int64
	// Addrs are the source addresses and networks the trunk's calls come
	// from ("203.0.113.7", "198.51.100.0/24", "2001:db8::/48"); empty for a
	// trunk that signs in.
	Addrs      []string
	Username   string // "" for a trunk known by its address
	HA1        string // MD5(username:realm:password), for the SIP core to check
	TechPrefix string
	// Security is what the trunk's calls must arrive over: "any", "tls", or
	// "cert" (TLS with a verified client certificate named CertName).
	Security, CertName string
	// MaxContacts is how many addresses a trunk that signs in may have
	// registered at once (0 counts as 1).
	MaxContacts int
	// NumberRules say how the client writes numbers, longest Strip first
	// (SortNumberRules): a dialled number takes the first rule whose Strip
	// it starts with. Empty = numbers are taken as they come.
	NumberRules []NumberRule
	// MaxDuration is the longest a call from this trunk may last, in
	// seconds; MaxCalls and MaxCPS the calls at once and a second it may
	// place. 0 = no limit of the trunk's own.
	MaxDuration, MaxCalls, MaxCPS int
	// Codecs are the audio codecs used with this client, in order of
	// preference; empty = whatever is offered.
	Codecs []string
	// Transcode lets this trunk's calls, and calls delivered to it, be
	// carried where the other side has no codec of Codecs: the media relay
	// translates (see Transcodes).
	Transcode bool
}

// NumberRule is one way a client writes numbers: a dialled number that
// starts with Strip loses it and gets Add in its place. An empty Strip
// matches every number, so it is the rule taken when no other matches.
type NumberRule struct {
	Strip string `json:"strip"`
	Add   string `json:"add"`
}

// SortNumberRules puts the longest Strip first, so that Dialled can stop
// at the first match: a client that writes both 00… and 0… has 00905…
// read by the 00 rule. The loader sorts once per snapshot, not per call.
func SortNumberRules(rules []NumberRule) {
	slices.SortFunc(rules, func(a, b NumberRule) int {
		if len(a.Strip) != len(b.Strip) {
			return len(b.Strip) - len(a.Strip)
		}
		return strings.Compare(a.Strip, b.Strip) // as the table keeps them
	})
}

// Dialled turns a number as the trunk's client writes it (the tech prefix
// already taken off) into the form tariffs and routes are written in. A
// number no rule matches is left as it is.
func (t Trunk) Dialled(number string) string {
	for _, r := range t.NumberRules {
		if rest, ok := strings.CutPrefix(number, r.Strip); ok {
			return r.Add + rest
		}
	}
	return number
}

// Admits applies the trunk's rule about how its requests must arrive, for
// calls and registrations alike: over transport ("udp", "tcp", "tls"), with
// tlsName the name in the certificate the SIP core verified ("" = none).
// It gives the reason when the request is not to be taken.
func (t Trunk) Admits(transport, tlsName string) (reason string, ok bool) {
	switch t.Security {
	case "tls":
		if transport != "tls" {
			return "tls required", false
		}
	case "cert":
		if transport != "tls" || tlsName == "" || tlsName != t.CertName {
			return "client certificate required", false
		}
	}
	return "", true
}

// Source is who a call came from, as the SIP core saw it.
type Source struct {
	AuthUser  string // user name the SIP core checked the password of, or ""
	IP        string
	Transport string // "udp", "tcp" or "tls"
	// TLSName is the common name of the certificate the caller presented and
	// the SIP core verified; "" when there was none.
	TLSName string
	// AnyTransport skips the trunk's transport rule: for callers that ask
	// what would happen to a call rather than carry one (the simulator).
	AnyTransport bool
	// Caller is the caller's number as it arrived; AnyCaller skips the
	// rules about it, for a question that names no caller.
	Caller    string
	AnyCaller bool
	// Asserted is the number the caller's side asserts for it (the user
	// part of P-Asserted-Identity); "" when there is none. Only rules on
	// the asserted number look at it.
	Asserted string
	// At is when the call starts, for rates and routes that depend on the
	// day or the hour; the zero value means now.
	At time.Time
	// Pick spreads calls over routes of equal standing by their weights
	// (see PickFor); zero keeps them in a fixed order, heaviest first.
	Pick uint64
}

type Client struct {
	ID, ResellerID          int64
	Active                  bool
	Balance, CreditLimit    int64
	TariffID, RoutingPlanID int64 // 0 = unset
	MaxCalls, MaxCPS        int
	// Fx is what one unit of the client's currency is worth in the
	// account's, in micro-units; 0 = the same currency.
	Fx int64
}

// CLIRule says what becomes of calls whose caller number starts with a
// prefix: "block" refuses them, "allow" lets them pass, "rewrite" lets them
// pass and changes the number the vendor is shown. "anonymous" refuses
// callers without a usable number (see callerDigits); it has no prefix.
type CLIRule struct {
	Action      string
	StripDigits int
	AddPrefix   string
}

// CLIRuleSet holds the rules about one of the caller's numbers by prefix: a
// client's own, and those of an account that apply to all its clients;
// and the clients and accounts that refuse callers without a number.
type CLIRuleSet struct {
	ByClient     map[int64]map[string]CLIRule
	ByReseller   map[int64]map[string]CLIRule
	AnonClient   map[int64]bool
	AnonReseller map[int64]bool
}

// CLIRules holds the rules on the number in From and those on the number
// the caller's side asserts (P-Asserted-Identity). Each set is applied to
// its own number; a call passes only if neither refuses it.
type CLIRules struct {
	From, Asserted CLIRuleSet
}

// Number sources of a rule, as stored (cli_rules.number_source).
const (
	NumberFrom     = "from"
	NumberAsserted = "pai"
)

// Add puts a rule in place: for client, or with client 0 for every client
// of reseller; on the number source names.
func (r *CLIRules) Add(reseller, client int64, source, prefix string, rule CLIRule) {
	set := &r.From
	if source == NumberAsserted {
		set = &r.Asserted
	}
	if rule.Action == "anonymous" {
		anon, key := &set.AnonReseller, reseller
		if client != 0 {
			anon, key = &set.AnonClient, client
		}
		if *anon == nil {
			*anon = map[int64]bool{}
		}
		(*anon)[key] = true
		return
	}
	by, key := &set.ByReseller, reseller
	if client != 0 {
		by, key = &set.ByClient, client
	}
	if *by == nil {
		*by = map[int64]map[string]CLIRule{}
	}
	if (*by)[key] == nil {
		(*by)[key] = map[string]CLIRule{}
	}
	(*by)[key][prefix] = rule
}

// callerDigits is the caller's number as rules see it: digits, without a
// leading "+". Anything else (empty, a name, "anonymous", a number with
// separators or letters) is no number at all.
func callerDigits(caller string) string {
	caller = strings.TrimPrefix(caller, "+")
	for i := 0; i < len(caller); i++ {
		if caller[i] < '0' || caller[i] > '9' {
			return ""
		}
	}
	return caller
}

// match finds the rule for a caller: the longest prefix decides, and at
// equal length the client's own rule goes before the account's. A caller
// without a number only meets rules with an empty prefix.
func (r CLIRuleSet) match(c Client, number string) (CLIRule, bool) {
	own, ownPrefix, ok1 := rating.LongestPrefix(r.ByClient[c.ID], number)
	all, allPrefix, ok2 := rating.LongestPrefix(r.ByReseller[c.ResellerID], number)
	if ok1 && (!ok2 || len(ownPrefix) >= len(allPrefix)) {
		return own, true
	}
	return all, ok2
}

// verdict is the rule that applies to number: the refusal of callers
// without one first (it stands above every prefix rule, the client's or
// the account's), then the longest prefix.
func (r CLIRuleSet) verdict(c Client, number string) (CLIRule, bool) {
	if len(r.ByClient) == 0 && len(r.ByReseller) == 0 && len(r.AnonClient) == 0 && len(r.AnonReseller) == 0 {
		return CLIRule{}, false // most installations have no rules on one of the numbers
	}
	if number == "" && (r.AnonClient[c.ID] || r.AnonReseller[c.ResellerID]) {
		return CLIRule{Action: "anonymous"}, true
	}
	return r.match(c, number)
}

// AnonymousCaller is the reason a caller without a usable number is refused
// with (433 Anonymity Disallowed, RFC 5079).
const AnonymousCaller = "anonymity disallowed"

type Connection struct {
	ID, VendorID     int64
	Host             string
	Port             int
	Transport        string
	TLSName          string // name the certificate must carry; "" = Host
	TechPrefix       string
	StripDigits      int
	MaxCalls, MaxCPS int
	VendorTariffID   int64
	Fx               int64 // the vendor's currency in the account's; 0 = the same
	Enabled          bool

	// AuthUser and AuthPassword are what the SIP core signs in to the vendor
	// with when it asks (401/407); AuthSealed is the password as stored,
	// which only a loader that holds the installation's key can open.
	AuthUser, AuthPassword string
	AuthSealed             []byte
	// NumberPlus sends the dialled number with a leading "+".
	NumberPlus bool
	// CLIStrip and CLIAdd change the caller's number as this vendor sees
	// it: digits taken off the front, then a prefix put there.
	CLIStrip int
	CLIAdd   string
	// PAIMode says what becomes of P-Asserted-Identity towards this vendor:
	// "pass" (as it came), "remove", or "caller" (the caller's number as
	// the vendor sees it). Empty counts as "pass".
	PAIMode string
	// RingTimeout is how long, in seconds, the vendor may take to answer
	// before the next route is tried; MaxDuration the longest a call over
	// this connection may last. 0 = no setting of the connection's own.
	RingTimeout, MaxDuration int
	// FailoverMode says which answers move a call on to the next route:
	// "standard" (or empty), "always", "never", or "custom" (FailoverCodes).
	FailoverMode  string
	FailoverCodes []int
	// OptionsInterval is the time between the engine's questions whether
	// the vendor is there, in seconds; 0 = not asked.
	OptionsInterval int
	// Codecs are the audio codecs offered to the vendor, in order of
	// preference; empty = whatever the caller offers.
	Codecs []string
	// Transcode lets calls whose trunk has no codec of Codecs be carried
	// here all the same, with the media relay translating (see Transcodes).
	Transcode bool
	// Register makes the SIP core register with the vendor (ADR-0035), as
	// AuthUser at RegisterDomain (Host when empty), asking for
	// RegisterExpires seconds.
	Register        bool
	RegisterExpires int
	RegisterDomain  string
}

// CallerFor is the caller's number as this vendor is to see it. A caller
// without a number (a name, "anonymous") is left as it is.
func (c Connection) CallerFor(caller string) string {
	if c.CLIStrip == 0 && c.CLIAdd == "" {
		return caller
	}
	digits := callerDigits(caller)
	if digits == "" {
		return caller
	}
	return c.CLIAdd + digits[min(c.CLIStrip, len(digits)):]
}

// MovesOn reports whether a call this connection failed with code is to
// try the next route. standard is what the rule without a setting says.
// A caller that gave up (487) never moves on.
func (c Connection) MovesOn(code int, standard bool) bool {
	if code == 487 {
		return false
	}
	switch c.FailoverMode {
	case "always":
		return true
	case "never":
		return false
	case "custom":
		return slices.Contains(c.FailoverCodes, code)
	}
	return standard
}

// CodecsFor is what to offer this vendor for a call from a trunk that
// keeps to trunk (its own list): the connection's codecs as far as the
// trunk takes them, in the connection's order; the trunk's when the
// connection has none of its own. Nil = no rule. ok is false when both
// have a list and nothing is on both: such a call has no codec to use.
func (c Connection) CodecsFor(trunk []string) (list []string, ok bool) {
	return CommonCodecs(c.Codecs, trunk)
}

// Transcodes reports whether a call between two sides with these codec
// lists needs its audio translated: both have a list and no codec is on
// both. Lists are what transcoding is decided on: the engine does not see
// the caller's offer, so a side without a list (it takes whatever is
// offered) never needs it. allowed is whether either side lets it happen;
// ok is false when the call cannot be carried at all.
func Transcodes(first, second []string, allowed bool) (transcode, ok bool) {
	if _, common := CommonCodecs(first, second); common {
		return false, true
	}
	return allowed, allowed
}

// CommonCodecs is first as far as second allows it; either may be empty
// (no rule of its own).
func CommonCodecs(first, second []string) (list []string, ok bool) {
	switch {
	case len(first) == 0:
		return second, true
	case len(second) == 0:
		return first, true
	}
	for _, name := range first {
		if slices.Contains(second, name) {
			list = append(list, name)
		}
	}
	return list, len(list) > 0
}

type Entry struct {
	ConnectionID     int64
	Priority, Weight int
	rating.Window    // when the route may be used, in the plan's time zone
}

type Plan struct {
	ID        int64
	Policy    string // "lcr" or "priority"
	AllowLoss bool
	Loc       *time.Location     // for the entries' windows; nil = UTC
	Cal       rating.Calendar    // holidays of the plan's account, for the windows
	Entries   map[string][]Entry // by prefix
}

// TimedTariff holds the prefixes of a tariff that have rates limited to
// days or hours: for each, every rate in force (tidied), with and without
// a window. Prefixes with a single rate for all hours are in Tariffs only.
type TimedTariff struct {
	Loc   *time.Location
	Cal   rating.Calendar // holidays of the tariff's account; nil = none
	Rates map[string][]rating.Rate
}

// Bundle is talk time a client gets free each period towards numbers that
// start with one of its prefixes. Seconds are billed seconds.
type Bundle struct {
	ID       int64
	Prefixes []string
	Seconds  int64
	// Loc is the time zone periods are counted in (the client's tariff's);
	// nil = UTC. Used is what the period Period had taken from the bundle
	// when the snapshot was loaded; any other period counts as untouched.
	Loc    *time.Location
	Period string
	Used   int64
}

// free is what is left of the bundle for a call starting at at, by the
// snapshot's count, and the period that call belongs to.
func (b *Bundle) free(at time.Time) (int64, string) {
	period := rating.Period(at, b.Loc)
	return b.Left(period), period
}

// Left is what the snapshot saw left of the bundle in a period. A period
// that began after the snapshot was loaded is untouched; one that is over
// gives nothing more, since its count is no longer read: a call that
// outlasts its period goes on at the balance's cost.
func (b *Bundle) Left(period string) int64 {
	switch {
	case period > b.Period:
		return b.Seconds
	case period < b.Period:
		return 0
	}
	return max(b.Seconds-b.Used, 0)
}

// BundleFor finds the client's bundle for number: the one with the longest
// matching prefix and, between equals, the one made first.
func (s *Snapshot) BundleFor(client int64, number string) (*Bundle, bool) {
	s.buildIndex()
	b, _, ok := rating.LongestPrefix(s.bundlesBy[client], number)
	return b, ok
}

// rate finds the rate of a tariff for number for a call starting at at:
// the longest prefix that has a rate for that moment.
func (s *Snapshot) rate(tariff int64, number string, at time.Time) (rating.Rate, string, bool) {
	base := s.Tariffs[tariff]
	timed, ok := s.Timed[tariff]
	if !ok {
		return rating.LongestPrefix(base, number)
	}
	local := at.In(timed.Loc)
	for i := len(number); i >= 0; i-- {
		if list, ok := timed.Rates[number[:i]]; ok {
			if r, ok := rating.Select(list, local, timed.Cal); ok {
				return r, number[:i], true
			}
			continue
		}
		if r, ok := base[number[:i]]; ok {
			return r, number[:i], true
		}
	}
	return rating.Rate{}, "", false
}

// Snapshot is an immutable view of the routing configuration.
type Snapshot struct {
	Trunks      []Trunk
	Clients     map[int64]Client
	Tariffs     map[int64]map[string]rating.Rate
	Timed       map[int64]TimedTariff // tariffs with rates by day or hour
	Plans       map[int64]Plan
	Connections map[int64]Connection
	CLIRules    CLIRules
	Firewall    Firewall           // the operator's rules about source addresses
	Bundles     map[int64][]Bundle // by client

	index     sync.Once
	bundlesBy map[int64]map[string]*Bundle // by client and prefix
	trunksBy  map[netip.Prefix][]Trunk     // by source address or network
	bits4     []int                        // lengths of the IPv4 networks in use, longest first
	bits6     []int                        // the same for IPv6
	usersBy   map[string][]Trunk           // by user name
	trunkIDs  map[int64]int                // place in Trunks, by id
	peers     map[string]bool              // vendor connection hosts
}

// Credentials returns what the SIP core needs to check a password for
// username, if a trunk signs in under that name.
func (s *Snapshot) Credentials(username string) (ha1 string, ok bool) {
	s.buildIndex()
	for _, t := range s.usersBy[username] {
		if t.HA1 != "" {
			return t.HA1, true
		}
	}
	return "", false
}

// Registrant returns the trunk that signs in as username, if it may
// register: one with a password, whatever its tech prefix.
func (s *Snapshot) Registrant(username string) (Trunk, bool) {
	s.buildIndex()
	for _, t := range s.usersBy[username] {
		if t.HA1 != "" {
			return t, true
		}
	}
	return Trunk{}, false
}

// Trunk returns the trunk with that id, if the snapshot has it (enabled,
// and with a password when it signs in).
func (s *Snapshot) Trunk(id int64) (Trunk, bool) {
	s.buildIndex()
	if i, ok := s.trunkIDs[id]; ok {
		return s.Trunks[i], true
	}
	return Trunk{}, false
}

// KnownSource reports whether ip is a client trunk's address, lies within
// one of a trunk's networks, or is a vendor connection's host.
func (s *Snapshot) KnownSource(ip string) bool {
	s.buildIndex()
	if s.peers[peerKey(ip)] {
		return true
	}
	addr, lengths, ok := s.source(ip)
	for i := 0; ok && i < len(lengths); i++ {
		if p, err := addr.Prefix(lengths[i]); err == nil && len(s.trunksBy[p]) > 0 {
			return true
		}
	}
	return false
}

// peerKey is a vendor's host, or a call's source address, as the index of
// vendors' addresses keeps it: an IPv6 address in its usual short form,
// whichever way it was written (an operator's "FD30:0::20", the SIP core's
// "FD30:0:0:0:0:0:0:20"). A name or an IPv4 address is kept as it is.
func peerKey(host string) string {
	if !strings.Contains(host, ":") {
		return host
	}
	a, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
	if err != nil {
		return host
	}
	return a.Unmap().WithZone("").String()
}

// uriHost is a host as a SIP URI carries it: an IPv6 address in brackets
// (RFC 3261 19.1.1, RFC 5118), anything else as it is.
func uriHost(host string) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]"
	}
	return host
}

// parseSource reads a trunk's address or network from text. A single
// address counts as a network of that address alone; bits set past the
// length of a network are dropped, and an IPv4 address written as IPv6
// ("::ffff:192.0.2.1") is taken as IPv4.
func parseSource(text string) (netip.Prefix, bool) {
	if p, err := netip.ParsePrefix(text); err == nil {
		if a := p.Addr(); a.Is4In6() && p.Bits() >= 96 {
			p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
		}
		return p.Masked(), true
	}
	a, err := netip.ParseAddr(text)
	if err != nil {
		return netip.Prefix{}, false
	}
	a = a.Unmap().WithZone("")
	return netip.PrefixFrom(a, a.BitLen()), true
}

// source reads a call's source address and gives the lengths of the
// networks it may lie in, longest first.
func (s *Snapshot) source(ip string) (netip.Addr, []int, bool) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return addr, nil, false
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is4() {
		return addr, s.bits4, true
	}
	return addr, s.bits6, true
}

func (s *Snapshot) buildIndex() {
	s.index.Do(func() {
		s.trunksBy = make(map[netip.Prefix][]Trunk, len(s.Trunks))
		s.usersBy = map[string][]Trunk{}
		s.trunkIDs = make(map[int64]int, len(s.Trunks))
		var seen4, seen6 [129]bool
		for i, t := range s.Trunks {
			s.trunkIDs[t.ID] = i
			if t.Username != "" {
				s.usersBy[t.Username] = append(s.usersBy[t.Username], t)
				continue
			}
			for _, text := range t.Addrs {
				p, ok := parseSource(text)
				if !ok {
					continue
				}
				s.trunksBy[p] = append(s.trunksBy[p], t)
				if p.Addr().Is4() {
					seen4[p.Bits()] = true
				} else {
					seen6[p.Bits()] = true
				}
			}
		}
		for n := 128; n >= 0; n-- {
			if seen4[n] {
				s.bits4 = append(s.bits4, n)
			}
			if seen6[n] {
				s.bits6 = append(s.bits6, n)
			}
		}
		s.bundlesBy = make(map[int64]map[string]*Bundle, len(s.Bundles))
		for client, list := range s.Bundles {
			by := map[string]*Bundle{}
			for i := range list {
				for _, p := range list[i].Prefixes {
					if old, ok := by[p]; !ok || list[i].ID < old.ID {
						by[p] = &list[i]
					}
				}
			}
			s.bundlesBy[client] = by
		}
		s.peers = make(map[string]bool, len(s.Connections))
		for _, c := range s.Connections {
			if c.Enabled {
				s.peers[peerKey(c.Host)] = true
			}
		}
	})
}

type Route struct {
	ConnectionID int64  `json:"connection_id"`
	VendorID     int64  `json:"vendor_id"`
	URI          string `json:"uri"`
	Number       string `json:"number"`
	BuyPrefix    string `json:"buy_prefix"`
	BuyPrice     int64  `json:"buy_price_per_min"`
	// BuyBase is the buying price in the account's currency: what vendors
	// in different currencies are compared by.
	BuyBase  int64 `json:"buy_price_base"`
	MaxCalls int   `json:"max_calls"`
	MaxCPS   int   `json:"max_cps"`
	// Step numbers the groups of routes that stand equal (same priority and,
	// in a least-cost plan, same price), in the order they are tried. Share
	// is the percentage of calls that try this route first within its step.
	Step  int `json:"step"`
	Share int `json:"share_pct"`

	// TrunkID, when set, makes this a route to a client's trunk instead of
	// a vendor's connection: the call is delivered to wherever that trunk
	// has registered from (see ToTrunk). The engine fills in URI and NextHop
	// from the registration when the call is placed.
	TrunkID int64 `json:"trunk_id,omitempty"`
	// NextHop is where the request is really sent when that is not where
	// URI points: the address a registration came from.
	NextHop string `json:"next_hop,omitempty"`
	// Transcode says the caller's trunk and this route have no codec in
	// common and the media relay is to translate between them. Such a
	// route counts against the engine's limit of calls transcoded at once.
	Transcode bool `json:"transcode,omitempty"`
	// DstClientID is the client a route to a trunk delivers to, and
	// ClientMaxCalls/ClientMaxCPS that client's own limits: a delivered call
	// counts against them as one the client placed would (ADR-0042). The
	// engine fills them in with NextHop.
	DstClientID    int64 `json:"dst_client_id,omitempty"`
	ClientMaxCalls int   `json:"client_max_calls,omitempty"`
	ClientMaxCPS   int   `json:"client_max_cps,omitempty"`
}

// ToTrunk is a route that delivers a call to a client's trunk at the
// address it has registered from, rather than to a vendor. Nothing in the
// core's own routing produces one: an extension puts it in a decision
// (engine.Config.Adjust) when a number belongs to a client.
func ToTrunk(trunkID int64, number string) Route {
	return Route{TrunkID: trunkID, Number: number, Step: 1, Share: 100}
}

type Decision struct {
	Allowed     bool   `json:"allowed"`
	SIPCode     int    `json:"sip_code"`
	Reason      string `json:"reason"`
	ClientID    int64  `json:"client_id,omitempty"`
	TrunkID     int64  `json:"trunk_id,omitempty"`
	Number      string `json:"number,omitempty"`
	SellPrefix  string `json:"sell_prefix,omitempty"`
	SellPrice   int64  `json:"sell_price_per_min,omitempty"`
	SellBase    int64  `json:"sell_price_base,omitempty"` // in the account's currency
	MaxDuration int    `json:"max_duration_sec,omitempty"`
	MaxCalls    int    `json:"client_max_calls,omitempty"`
	MaxCPS      int    `json:"client_max_cps,omitempty"`
	// The trunk's own limits on calls at once and calls a second; 0 = none.
	TrunkMaxCalls int     `json:"trunk_max_calls,omitempty"`
	TrunkMaxCPS   int     `json:"trunk_max_cps,omitempty"`
	Routes        []Route `json:"routes,omitempty"`
	// Caller is the number to show the vendor as the caller, when a rule
	// changed it; empty leaves what arrived untouched.
	Caller string `json:"caller,omitempty"`
	// Inbound says the call came through no trunk: an extension took it on
	// behalf of the client (AuthorizeFor). TrunkID is then 0.
	Inbound bool `json:"inbound,omitempty"`

	// BundleID is the bundle of minutes the call draws on before it costs
	// money (0 = none), BundleFree what the snapshot saw left of it, in
	// seconds, for the period the call starts in.
	BundleID   int64 `json:"bundle_id,omitempty"`
	BundleFree int64 `json:"bundle_free_sec,omitempty"`

	// What the engine needs to reserve funds for the call: the selling rate
	// and the client's balance plus credit as the snapshot saw them.
	Sell         rating.Rate `json:"-"`
	Funds        int64       `json:"-"`
	BundlePeriod string      `json:"-"`
}

func reject(code int, reason string) Decision {
	return Decision{SIPCode: code, Reason: reason}
}

// Authorize decides whether a call from srcIP to dst may proceed and over
// which vendor connections, in order of preference.
func (s *Snapshot) Authorize(srcIP, dst string) Decision {
	return s.AuthorizeFrom(Source{IP: srcIP, AnyTransport: true, AnyCaller: true}, dst)
}

// AuthorizeAs does the same for a caller the SIP core has authenticated as
// authUser; the source address then plays no part. An empty authUser means
// the caller is known by its address only.
func (s *Snapshot) AuthorizeAs(authUser, srcIP, dst string) Decision {
	return s.AuthorizeFrom(Source{AuthUser: authUser, IP: srcIP, AnyTransport: true, AnyCaller: true}, dst)
}

// AuthorizeFrom decides a call from everything the SIP core knows about
// its source, including how it arrived.
func (s *Snapshot) AuthorizeFrom(src Source, dst string) Decision {
	trunk, ok := s.matchTrunk(src.AuthUser, src.IP, dst)
	if !ok {
		return reject(403, UnknownSource)
	}
	return s.decide(trunk, src, trunk.Dialled(strings.TrimPrefix(dst, trunk.TechPrefix)), nil)
}

// UnknownSource is the reason a call is refused with when its source
// matches no trunk; such a refusal names no client.
const UnknownSource = "unknown source"

// Inbound is a call whose source matches no trunk, taken by an extension on
// behalf of one of the snapshot's clients (engine.Config.Incoming): a
// carrier handing in a call to a number a client owns, say. The client pays
// for the call, its limits apply and its records name it; the call has no
// trunk, so no trunk's rules, limits or codecs apply.
type Inbound struct {
	// ClientID is the client the call is taken on behalf of; a client the
	// snapshot does not have leaves the call unknown.
	ClientID int64
	// Number is the number the call is for, in the form tariffs and routes
	// are written in; empty = as dialled.
	Number string
	// Rate, if set, is what the client pays for the call instead of its
	// tariff's price for the number. It is what the call's reservation is
	// priced by; the record is priced by the rater as any other (an
	// extension that prices it differently does so there too: api.OnCDRPrice).
	Rate *rating.Rate
	// Routes, if set, are where the call goes instead of the routes of the
	// client's plan: routing.ToTrunk to deliver it to a client's trunk.
	Routes []Route
}

// AuthorizeFor decides a call that no trunk matches on behalf of the client
// in names, as if that client had placed it from a trunk with no rules of
// its own: the client's caller rules, tariff, bundles, funds and plan apply,
// except where in says otherwise.
func (s *Snapshot) AuthorizeFor(in Inbound, src Source, dst string) Decision {
	if _, ok := s.Clients[in.ClientID]; !ok || in.ClientID == 0 {
		return reject(403, UnknownSource)
	}
	number := in.Number
	if number == "" {
		number = dst
	}
	return s.decide(Trunk{ClientID: in.ClientID}, src, number, &in)
}

// decide is the decision on a call to number from trunk's client; in, when
// set, is a call that came through no trunk (trunk is then the zero trunk
// of that client).
func (s *Snapshot) decide(trunk Trunk, src Source, number string, in *Inbound) Decision {
	inbound := in != nil
	// Rejections past this point name the client so they can be recorded.
	reject := func(code int, reason string) Decision {
		return Decision{SIPCode: code, Reason: reason, ClientID: trunk.ClientID, TrunkID: trunk.ID, Number: number, Inbound: inbound}
	}
	if !src.AnyTransport {
		if reason, ok := trunk.Admits(src.Transport, src.TLSName); !ok {
			return reject(403, reason)
		}
	}
	client, ok := s.Clients[trunk.ClientID]
	if !ok || !client.Active {
		return reject(403, "account inactive")
	}
	caller := ""
	if !src.AnyCaller {
		// Each set of rules judges its own number; a refusal by either
		// refuses the call, a missing number before a barred one. Only the
		// rules on From change the number shown.
		from := callerDigits(src.Caller)
		fr, fok := s.CLIRules.From.verdict(client, from)
		ar, aok := s.CLIRules.Asserted.verdict(client, callerDigits(src.Asserted))
		switch {
		case fok && fr.Action == "anonymous", aok && ar.Action == "anonymous":
			return reject(433, AnonymousCaller)
		case fok && fr.Action == "block", aok && ar.Action == "block":
			return reject(403, "caller not allowed")
		case fok && fr.Action == "rewrite" && from != "":
			caller = fr.AddPrefix + from[min(fr.StripDigits, len(from)):]
			if caller == src.Caller {
				caller = ""
			}
		}
	}

	at := src.At
	if at.IsZero() {
		at = time.Now()
	}
	sell, sellPrefix, ok := s.rate(client.TariffID, number, at)
	if inbound && in.Rate != nil {
		sell, sellPrefix, ok = *in.Rate, in.Rate.Prefix, true
	}
	if !ok || sell.Blocked {
		return reject(403, "destination not allowed")
	}
	// A bundle pays for the call before the balance does; a call that costs
	// nothing takes nothing from it.
	var bundle *Bundle
	var bundleFree int64
	var bundlePeriod string
	if !sell.Free() {
		if b, ok := s.BundleFor(client.ID, number); ok {
			bundle = b
			bundleFree, bundlePeriod = b.free(at)
		}
	}
	maxDur := sell.MaxSecondsAfter(client.Balance+client.CreditLimit, bundleFree, MaxCallSeconds)
	if maxDur <= 0 {
		return reject(402, "insufficient balance")
	}
	if trunk.MaxDuration > 0 {
		maxDur = min(maxDur, trunk.MaxDuration)
	}

	sellBase := rating.Convert(sell.PricePerMin, client.Fx)
	d := Decision{
		Allowed: true, SIPCode: 200, Reason: "ok",
		ClientID: client.ID, TrunkID: trunk.ID, Number: number,
		SellPrefix: sellPrefix, SellPrice: sell.PricePerMin, SellBase: sellBase, MaxDuration: maxDur,
		MaxCalls: client.MaxCalls, MaxCPS: client.MaxCPS,
		TrunkMaxCalls: trunk.MaxCalls, TrunkMaxCPS: trunk.MaxCPS,
		Sell: sell, Funds: client.Balance + client.CreditLimit,
		Caller: caller, Inbound: inbound,
	}
	if bundle != nil {
		d.BundleID, d.BundleFree, d.BundlePeriod = bundle.ID, bundleFree, bundlePeriod
	}
	if inbound && len(in.Routes) > 0 {
		// The extension has said where the call goes: the plan is not asked.
		d.Routes = slices.Clone(in.Routes)
		return d
	}

	plan, ok := s.Plans[client.RoutingPlanID]
	if !ok {
		return reject(503, "no routing plan")
	}
	entries, _, _ := rating.LongestPrefix(plan.Entries, number)

	type cand struct {
		Route
		entry Entry
	}
	var cands []cand
	planTime := at.UTC()
	if plan.Loc != nil {
		planTime = at.In(plan.Loc)
	}
	for _, e := range entries {
		conn, ok := s.Connections[e.ConnectionID]
		if !ok || !conn.Enabled {
			continue
		}
		if e.Limited() && !e.Covers(planTime, plan.Cal) {
			continue
		}
		buy, buyPrefix, ok := s.rate(conn.VendorTariffID, number, at)
		if !ok || buy.Blocked {
			continue
		}
		// Vendors may be paid in other currencies than the client pays
		// in: prices are compared in the account's.
		buyBase := rating.Convert(buy.PricePerMin, conn.Fx)
		if !plan.AllowLoss && buyBase > sellBase {
			continue
		}
		out := number
		if conn.StripDigits < len(out) {
			out = out[conn.StripDigits:]
		}
		out = conn.TechPrefix + out
		if conn.NumberPlus {
			out = "+" + out
		}
		// A trunk and a connection that share no codec cannot carry a
		// call between them, unless one of them lets the media relay
		// translate.
		transcode, ok := Transcodes(conn.Codecs, trunk.Codecs, conn.Transcode || trunk.Transcode)
		if !ok {
			continue
		}
		cands = append(cands, cand{entry: e, Route: Route{
			ConnectionID: conn.ID, VendorID: conn.VendorID,
			URI:    "sip:" + out + "@" + uriHost(conn.Host) + ":" + itoa(conn.Port) + ";transport=" + conn.Transport,
			Number: out, BuyPrefix: buyPrefix, BuyPrice: buy.PricePerMin, BuyBase: buyBase,
			MaxCalls: conn.MaxCalls, MaxCPS: conn.MaxCPS, Transcode: transcode,
		}})
	}
	if len(cands) == 0 {
		return reject(503, "no route")
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if plan.Policy == "lcr" && a.BuyBase != b.BuyBase {
			return a.BuyBase < b.BuyBase
		}
		if a.entry.Priority != b.entry.Priority {
			return a.entry.Priority < b.entry.Priority
		}
		if a.entry.Weight != b.entry.Weight {
			return a.entry.Weight > b.entry.Weight
		}
		return a.ConnectionID < b.ConnectionID
	})
	// Routes that stand equal form a step and share its calls by weight.
	rnd := src.Pick
	for start, step := 0, 1; start < len(cands); step++ {
		end, total := start, uint64(0)
		for end < len(cands) && cands[end].entry.Priority == cands[start].entry.Priority &&
			(plan.Policy != "lcr" || cands[end].BuyBase == cands[start].BuyBase) {
			total += weight(cands[end].entry)
			end++
		}
		for i := start; i < end; i++ {
			cands[i].Step = step
			switch w := weight(cands[i].entry); {
			case total > 0:
				cands[i].Share = int((w*100 + total/2) / total)
			case end-start == 1:
				cands[i].Share = 100
			}
		}
		// A weighted draw without replacement: each place in turn goes to
		// one of the routes still left, by weight. Routes of weight zero
		// keep their places at the end.
		for i := start; src.Pick != 0 && i < end-1 && total > 0; i++ {
			rnd += 0x9e3779b97f4a7c15
			z := rnd
			z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
			z = (z ^ z>>27) * 0x94d049bb133111eb
			at, j := (z^z>>31)%total, i
			for w := weight(cands[j].entry); at >= w; w = weight(cands[j].entry) {
				at -= w
				j++
			}
			won := cands[j]
			copy(cands[i+1:j+1], cands[i:j])
			cands[i] = won
			total -= weight(won.entry)
		}
		start = end
	}

	for _, c := range cands {
		d.Routes = append(d.Routes, c.Route)
	}
	return d
}

func weight(e Entry) uint64 {
	if e.Weight <= 0 {
		return 0
	}
	return uint64(e.Weight)
}

// PickFor turns a call's identifier into the number that spreads calls over
// routes of equal standing: the same call always gets the same order.
func PickFor(callID string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(callID); i++ {
		h = (h ^ uint64(callID[i])) * 1099511628211
	}
	if h == 0 {
		return 1
	}
	return h
}

// matchTrunk picks the trunk a call belongs to. A caller that signed in is
// looked up by its user name alone. Otherwise the trunks whose addresses or
// networks hold srcIP are candidates, as far as their tech prefix starts
// dst: the most specific network wins (a single address before a network,
// a /28 before a /24), and among trunks of the same network the longest
// tech prefix.
func (s *Snapshot) matchTrunk(authUser, srcIP, dst string) (Trunk, bool) {
	s.buildIndex()
	if authUser != "" {
		return longestTech(s.usersBy[authUser], dst)
	}
	addr, lengths, ok := s.source(srcIP)
	if !ok {
		return Trunk{}, false
	}
	for _, n := range lengths {
		p, err := addr.Prefix(n)
		if err != nil {
			continue
		}
		if t, ok := longestTech(s.trunksBy[p], dst); ok {
			return t, true
		}
	}
	return Trunk{}, false
}

// longestTech picks, among trunks, the one whose tech prefix is the longest
// one dst starts with.
func longestTech(candidates []Trunk, dst string) (best Trunk, found bool) {
	for _, t := range candidates {
		if !strings.HasPrefix(dst, t.TechPrefix) {
			continue
		}
		if !found || len(t.TechPrefix) > len(best.TechPrefix) {
			best, found = t, true
		}
	}
	return best, found
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
