// SPDX-License-Identifier: AGPL-3.0-only

// Package trace keeps the signalling of calls: it takes the copies the SIP
// core sends over HEP, stores them in ClickHouse, reads a call's messages
// back and writes them out as a capture file.
package trace

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net/netip"
	"time"
)

// Message is one SIP message as it crossed the SIP core.
type Message struct {
	At        time.Time
	Seq       uint64 // order of arrival at the collector; breaks ties in At
	CallID    string
	Direction string // "in": received by the SIP core, "out": sent by it, "": unknown
	Transport string // udp, tcp, tls, ws, sctp
	Src, Dst  netip.AddrPort
	Raw       []byte
}

// HEP3 chunk types (vendor 0).
const (
	chunkFamily      = 1
	chunkProto       = 2
	chunkSrc4        = 3
	chunkDst4        = 4
	chunkSrc6        = 5
	chunkDst6        = 6
	chunkSrcPort     = 7
	chunkDstPort     = 8
	chunkSec         = 9
	chunkUsec        = 10
	chunkType        = 11
	chunkPayload     = 15
	chunkCorrelation = 17
)

var (
	errNotHEP = errors.New("not a HEP3 packet")
	errNotSIP = errors.New("not a SIP message")
	errNoCall = errors.New("message without a Call-ID")
)

// transports maps what the SIP core writes as the IP protocol to a
// transport. HEP has no number for TLS or WebSocket; OpenSIPS borrows two
// protocols that never carry SIP (IDP and ESP).
var transports = map[byte]string{17: "udp", 6: "tcp", 22: "tls", 50: "ws", 132: "sctp"}

// ParseHEP reads one HEP3 packet that carries a SIP message. The message's
// bytes are copied: the packet's buffer may be used again.
func ParseHEP(pkt []byte) (Message, error) {
	var m Message
	if len(pkt) < 6 || string(pkt[:4]) != "HEP3" {
		return m, errNotHEP
	}
	if n := int(binary.BigEndian.Uint16(pkt[4:])); n < 6 || n > len(pkt) {
		return m, errNotHEP
	} else {
		pkt = pkt[6:n]
	}
	var srcIP, dstIP netip.Addr
	var srcPort, dstPort uint16
	var sec, usec uint32
	var payload, correlation []byte
	kind := byte(1)
	m.Transport = "udp"
	for len(pkt) > 0 {
		if len(pkt) < 6 {
			return m, errNotHEP
		}
		vendor, id, n := binary.BigEndian.Uint16(pkt), binary.BigEndian.Uint16(pkt[2:]), int(binary.BigEndian.Uint16(pkt[4:]))
		if n < 6 || n > len(pkt) {
			return m, errNotHEP
		}
		data := pkt[6:n]
		pkt = pkt[n:]
		if vendor != 0 {
			continue
		}
		switch {
		case id == chunkProto && len(data) == 1:
			if t, ok := transports[data[0]]; ok {
				m.Transport = t
			}
		case id == chunkSrc4 && len(data) == 4, id == chunkSrc6 && len(data) == 16:
			srcIP, _ = netip.AddrFromSlice(data)
		case id == chunkDst4 && len(data) == 4, id == chunkDst6 && len(data) == 16:
			dstIP, _ = netip.AddrFromSlice(data)
		case id == chunkSrcPort && len(data) == 2:
			srcPort = binary.BigEndian.Uint16(data)
		case id == chunkDstPort && len(data) == 2:
			dstPort = binary.BigEndian.Uint16(data)
		case id == chunkSec && len(data) == 4:
			sec = binary.BigEndian.Uint32(data)
		case id == chunkUsec && len(data) == 4:
			usec = binary.BigEndian.Uint32(data)
		case id == chunkType && len(data) == 1:
			kind = data[0]
		case id == chunkPayload:
			payload = data
		case id == chunkCorrelation:
			correlation = data
		}
	}
	if kind != 1 || len(payload) == 0 || !srcIP.IsValid() || !dstIP.IsValid() {
		return m, errNotSIP
	}
	m.CallID = CallID(payload)
	if m.CallID == "" {
		m.CallID = string(correlation)
	}
	if m.CallID == "" {
		return m, errNoCall
	}
	m.Src, m.Dst = netip.AddrPortFrom(srcIP.Unmap(), srcPort), netip.AddrPortFrom(dstIP.Unmap(), dstPort)
	m.At = time.Unix(int64(sec), int64(usec)*1000).UTC()
	if sec == 0 {
		m.At = time.Now().UTC()
	}
	m.Raw = bytes.Clone(payload)
	return m, nil
}

// headers calls f with the name and the value of every header line of a SIP
// message, stopping at the body. The slices point into raw.
func headers(raw []byte, f func(name, value []byte, start, end int)) {
	pos := bytes.IndexByte(raw, '\n') + 1 // past the first line
	if pos == 0 {
		return
	}
	for pos < len(raw) {
		end := bytes.IndexByte(raw[pos:], '\n')
		next := len(raw)
		if end >= 0 {
			next = pos + end + 1
		}
		if len(bytes.TrimRight(raw[pos:next], "\r\n")) == 0 {
			return
		}
		// A line that starts with white space continues the header before it.
		for next < len(raw) && (raw[next] == ' ' || raw[next] == '\t') {
			if end = bytes.IndexByte(raw[next:], '\n'); end < 0 {
				next = len(raw)
			} else {
				next += end + 1
			}
		}
		line := bytes.TrimRight(raw[pos:next], "\r\n")
		if colon := bytes.IndexByte(line, ':'); colon > 0 {
			f(bytes.TrimSpace(line[:colon]), bytes.TrimSpace(line[colon+1:]), pos, pos+len(line))
		}
		pos = next
	}
}

// CallID returns the Call-ID of a SIP message, or "".
func CallID(raw []byte) string {
	var id string
	headers(raw, func(name, value []byte, _, _ int) {
		if id == "" && (bytes.EqualFold(name, []byte("Call-ID")) || bytes.EqualFold(name, []byte("i"))) {
			id = string(value)
		}
	})
	return id
}

// FirstLine is the request or status line of a SIP message.
func FirstLine(raw []byte) string {
	line, _, _ := bytes.Cut(raw, []byte("\n"))
	return string(bytes.TrimRight(line, "\r"))
}
