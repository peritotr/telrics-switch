// SPDX-License-Identifier: AGPL-3.0-only

package trace

import (
	"encoding/binary"
	"io"
	"net/netip"
)

// WritePcap writes messages as a capture file (classic pcap, Ethernet) that
// Wireshark and tcpdump read. Nothing here was captured off the wire: each
// message gets an Ethernet, IP and UDP or TCP header made up from what is
// known about it, so that the usual tools can show the call. Messages that
// travelled over TLS or WebSocket are written as plain TCP with the text the
// SIP core saw after decryption.
//
// TCP streams have no handshake; sequence numbers start at 1 and advance
// with the bytes of each direction, so a stream reassembles.
func WritePcap(w io.Writer, msgs []Message) error {
	head := make([]byte, 24)
	binary.LittleEndian.PutUint32(head[0:], 0xa1b2c3d4) // microsecond timestamps
	binary.LittleEndian.PutUint16(head[4:], 2)
	binary.LittleEndian.PutUint16(head[6:], 4)
	binary.LittleEndian.PutUint32(head[16:], 262144) // snap length
	binary.LittleEndian.PutUint32(head[20:], 1)      // LINKTYPE_ETHERNET
	if _, err := w.Write(head); err != nil {
		return err
	}
	type flow struct{ src, dst netip.AddrPort }
	next := map[flow]uint32{} // next sequence number of each direction
	var ident uint16
	for _, m := range msgs {
		if !m.Src.IsValid() || !m.Dst.IsValid() || m.Src.Addr().Is4() != m.Dst.Addr().Is4() {
			continue
		}
		if m.Transport == "udp" || m.Transport == "sctp" {
			ident++
			if err := writeFrame(w, m, ident, udpSegment(m, m.Raw), 17); err != nil {
				return err
			}
			continue
		}
		out, back := flow{m.Src, m.Dst}, flow{m.Dst, m.Src}
		if next[out] == 0 {
			next[out] = 1
		}
		if next[back] == 0 {
			next[back] = 1
		}
		for rest := m.Raw; len(rest) > 0; {
			n := min(len(rest), tcpSegmentSize)
			ident++
			if err := writeFrame(w, m, ident, tcpSegment(m, rest[:n], next[out], next[back], n == len(rest)), 6); err != nil {
				return err
			}
			next[out] += uint32(n)
			rest = rest[n:]
		}
	}
	return nil
}

const tcpSegmentSize = 1400

// macs are locally administered addresses: one for whoever sends a frame,
// one for whoever receives it.
var macSrc, macDst = []byte{2, 0, 0, 0, 0, 1}, []byte{2, 0, 0, 0, 0, 2}

func writeFrame(w io.Writer, m Message, ident uint16, segment []byte, proto byte) error {
	frame := make([]byte, 0, 16+14+40+len(segment))
	frame = frame[:16] // record header, filled in below
	frame = append(append(frame, macDst...), macSrc...)
	src, dst := m.Src.Addr(), m.Dst.Addr()
	if src.Is4() {
		frame = append(frame, 0x08, 0x00)
		ip := make([]byte, 20)
		ip[0] = 0x45
		binary.BigEndian.PutUint16(ip[2:], uint16(20+len(segment)))
		binary.BigEndian.PutUint16(ip[4:], ident)
		ip[6] = 0x40 // don't fragment
		ip[8] = 64
		ip[9] = proto
		a, b := src.As4(), dst.As4()
		copy(ip[12:], a[:])
		copy(ip[16:], b[:])
		binary.BigEndian.PutUint16(ip[10:], ^fold(sum(0, ip)))
		frame = append(frame, ip...)
	} else {
		frame = append(frame, 0x86, 0xdd)
		ip := make([]byte, 40)
		ip[0] = 0x60
		binary.BigEndian.PutUint16(ip[4:], uint16(len(segment)))
		ip[6] = proto
		ip[7] = 64
		a, b := src.As16(), dst.As16()
		copy(ip[8:], a[:])
		copy(ip[24:], b[:])
		frame = append(frame, ip...)
	}
	frame = append(frame, segment...)
	usec := m.At.UnixMicro()
	binary.LittleEndian.PutUint32(frame[0:], uint32(usec/1e6))
	binary.LittleEndian.PutUint32(frame[4:], uint32(usec%1e6))
	binary.LittleEndian.PutUint32(frame[8:], uint32(len(frame)-16))
	binary.LittleEndian.PutUint32(frame[12:], uint32(len(frame)-16))
	_, err := w.Write(frame)
	return err
}

func udpSegment(m Message, payload []byte) []byte {
	// An IPv4 packet holds 65535 bytes at most; nothing the SIP core can
	// send over HEP is longer.
	payload = payload[:min(len(payload), 65535-20-8)]
	seg := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint16(seg[0:], m.Src.Port())
	binary.BigEndian.PutUint16(seg[2:], m.Dst.Port())
	binary.BigEndian.PutUint16(seg[4:], uint16(8+len(payload)))
	seg = append(seg, payload...)
	c := ^fold(sum(pseudo(m, 17, len(seg)), seg))
	if c == 0 {
		c = 0xffff
	}
	binary.BigEndian.PutUint16(seg[6:], c)
	return seg
}

func tcpSegment(m Message, payload []byte, seq, ack uint32, last bool) []byte {
	seg := make([]byte, 20, 20+len(payload))
	binary.BigEndian.PutUint16(seg[0:], m.Src.Port())
	binary.BigEndian.PutUint16(seg[2:], m.Dst.Port())
	binary.BigEndian.PutUint32(seg[4:], seq)
	binary.BigEndian.PutUint32(seg[8:], ack)
	seg[12] = 5 << 4
	seg[13] = 0x10 // ACK
	if last {
		seg[13] |= 0x08 // PSH
	}
	binary.BigEndian.PutUint16(seg[14:], 65535)
	seg = append(seg, payload...)
	binary.BigEndian.PutUint16(seg[16:], ^fold(sum(pseudo(m, 6, len(seg)), seg)))
	return seg
}

// pseudo starts a UDP or TCP checksum with the addresses, the protocol and
// the length, as both IP versions prescribe.
func pseudo(m Message, proto byte, length int) uint32 {
	var s uint32
	if m.Src.Addr().Is4() {
		a, b := m.Src.Addr().As4(), m.Dst.Addr().As4()
		s = sum(sum(0, a[:]), b[:])
	} else {
		a, b := m.Src.Addr().As16(), m.Dst.Addr().As16()
		s = sum(sum(0, a[:]), b[:])
	}
	return s + uint32(proto) + uint32(length)
}

func sum(s uint32, b []byte) uint32 {
	for ; len(b) >= 2; b = b[2:] {
		s += uint32(b[0])<<8 | uint32(b[1])
	}
	if len(b) == 1 {
		s += uint32(b[0]) << 8
	}
	return s
}

func fold(s uint32) uint16 {
	for s>>16 != 0 {
		s = s&0xffff + s>>16
	}
	return uint16(s)
}
