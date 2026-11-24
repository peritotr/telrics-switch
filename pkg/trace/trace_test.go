// SPDX-License-Identifier: AGPL-3.0-only

package trace

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// hep builds a HEP3 packet the way the SIP core does.
func hep(proto byte, src, dst string, at time.Time, payload string) []byte {
	var body []byte
	chunk := func(id uint16, data []byte) {
		body = binary.BigEndian.AppendUint16(body, 0)
		body = binary.BigEndian.AppendUint16(body, id)
		body = binary.BigEndian.AppendUint16(body, uint16(6+len(data)))
		body = append(body, data...)
	}
	s, d := netip.MustParseAddrPort(src), netip.MustParseAddrPort(dst)
	if s.Addr().Is4() {
		chunk(chunkFamily, []byte{2})
		chunk(chunkSrc4, s.Addr().AsSlice())
		chunk(chunkDst4, d.Addr().AsSlice())
	} else {
		chunk(chunkFamily, []byte{10})
		chunk(chunkSrc6, s.Addr().AsSlice())
		chunk(chunkDst6, d.Addr().AsSlice())
	}
	chunk(chunkProto, []byte{proto})
	chunk(chunkSrcPort, binary.BigEndian.AppendUint16(nil, s.Port()))
	chunk(chunkDstPort, binary.BigEndian.AppendUint16(nil, d.Port()))
	chunk(chunkSec, binary.BigEndian.AppendUint32(nil, uint32(at.Unix())))
	chunk(chunkUsec, binary.BigEndian.AppendUint32(nil, uint32(at.Nanosecond()/1000)))
	chunk(chunkType, []byte{1})
	chunk(12, []byte{0, 0, 0, 1})              // capture id: ignored
	body = append(body, 0, 9, 0, 1, 0, 7, 'x') // another vendor's chunk: ignored
	chunk(chunkPayload, []byte(payload))
	pkt := append([]byte("HEP3"), 0, 0)
	binary.BigEndian.PutUint16(pkt[4:], uint16(6+len(body)))
	return append(pkt, body...)
}

const invite = "INVITE sip:905551112233@172.30.0.10 SIP/2.0\r\n" +
	"Via: SIP/2.0/UDP 172.30.0.21:5060;branch=z9hG4bK-1\r\n" +
	"From: <sip:a@172.30.0.21>;tag=1\r\nTo: <sip:905551112233@172.30.0.10>\r\n" +
	"Call-ID: test-1@172.30.0.21\r\nCSeq: 2 INVITE\r\n" +
	"Authorization: Digest username=\"lab\",realm=\"softswitch\",nonce=\"abc\",uri=\"sip:x\",response=\"0123456789abcdef0123456789abcdef\",algorithm=MD5\r\n" +
	"Content-Length: 4\r\n\r\nv=0\n"

func TestParseHEP(t *testing.T) {
	at := time.Date(2026, 11, 22, 10, 0, 0, 123456000, time.UTC)
	m, err := ParseHEP(hep(17, "172.30.0.21:5060", "172.30.0.10:5060", at, invite))
	if err != nil {
		t.Fatal(err)
	}
	if m.CallID != "test-1@172.30.0.21" || m.Transport != "udp" || !m.At.Equal(at) ||
		m.Src.String() != "172.30.0.21:5060" || m.Dst.String() != "172.30.0.10:5060" || string(m.Raw) != invite {
		t.Fatalf("parsed %+v", m)
	}
	if got := FirstLine(m.Raw); got != "INVITE sip:905551112233@172.30.0.10 SIP/2.0" {
		t.Fatalf("first line %q", got)
	}
	// TLS and IPv6; the compact form of Call-ID.
	m, err = ParseHEP(hep(22, "[2001:db8::1]:40000", "[2001:db8::2]:5061", at, "SIP/2.0 200 OK\r\ni: short\r\n\r\n"))
	if err != nil || m.Transport != "tls" || m.CallID != "short" || m.Src.Addr().String() != "2001:db8::1" {
		t.Fatalf("parsed %+v, %v", m, err)
	}
	// A Call-ID in the body is not the message's.
	if _, err := ParseHEP(hep(17, "10.0.0.1:1", "10.0.0.2:2", at, "OPTIONS sip:x SIP/2.0\r\nVia: x\r\n\r\nCall-ID: body\r\n")); err != errNoCall {
		t.Fatalf("body Call-ID: %v", err)
	}
	// Anything damaged is refused, never read past its end.
	good := hep(17, "10.0.0.1:1", "10.0.0.2:2", at, invite)
	for n := 0; n < len(good); n++ {
		if _, err := ParseHEP(good[:n]); err == nil {
			t.Fatalf("packet cut at %d accepted", n)
		}
	}
	for i := 4; i < 40; i++ {
		bad := bytes.Clone(good)
		bad[i] ^= 0xff
		ParseHEP(bad) // must not panic
	}
	if _, err := ParseHEP([]byte("INVITE sip:x SIP/2.0\r\n")); err != errNotHEP {
		t.Fatalf("plain SIP: %v", err)
	}
}

func TestMask(t *testing.T) {
	out := string(Mask([]byte(invite)))
	if strings.Contains(out, "0123456789abcdef") {
		t.Fatalf("response left in:\n%s", out)
	}
	for _, keep := range []string{`username="lab"`, `nonce="abc"`, `response="***",algorithm=MD5`, "Content-Length: 4\r\n\r\nv=0\n", "CSeq: 2 INVITE\r\n"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("%q lost:\n%s", keep, out)
		}
	}
	cases := map[string]string{
		// Bare value, other case, the proxy's header, a folded line.
		"X sip:a SIP/2.0\r\nproxy-authorization: digest username=u, RESPONSE=deadbeef, qop=auth\r\n\r\n":      "proxy-authorization: digest username=u, RESPONSE=\"***\", qop=auth\r\n",
		"X sip:a SIP/2.0\r\nAuthorization: Digest username=\"u\",\r\n response=\"deadbeef\"\r\nTo: x\r\n\r\n": "response=\"***\"\r\nTo: x\r\n",
		// Other schemes carry nothing but the secret.
		"X sip:a SIP/2.0\r\nAuthorization: Basic dXNlcjpwYXNz\r\n\r\n":      "Authorization: Basic ***\r\n",
		"X sip:a SIP/2.0\r\nAuthorization: Bearer abc.def.ghi\r\nTo: x\r\n": "Authorization: Bearer ***\r\nTo: x\r\n",
	}
	for in, want := range cases {
		got := string(Mask([]byte(in)))
		if !strings.Contains(got, want) || strings.Contains(got, "deadbeef") || strings.Contains(got, "dXNl") || strings.Contains(got, "abc.def") {
			t.Errorf("Mask(%q) = %q, want it to contain %q", in, got, want)
		}
	}
	// A body that looks like credentials is left alone, as is a message without any.
	body := "X sip:a SIP/2.0\r\nTo: x\r\n\r\nAuthorization: Digest response=\"deadbeef\"\r\n"
	if got := string(Mask([]byte(body))); got != body {
		t.Errorf("body changed: %q", got)
	}
}

func sample() []Message {
	at := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	ap := netip.MustParseAddrPort
	long := "INVITE sip:1@198.51.100.2 SIP/2.0\r\nCall-ID: c1\r\nCSeq: 1 INVITE\r\nX-Pad: " + strings.Repeat("p", 3000) + "\r\nContent-Length: 0\r\n\r\n"
	return []Message{
		{At: at, Transport: "udp", Src: ap("192.0.2.1:5060"), Dst: ap("192.0.2.10:5060"), Raw: []byte("INVITE sip:1@192.0.2.10 SIP/2.0\r\nCall-ID: c1\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n")},
		{At: at.Add(1500 * time.Microsecond), Transport: "tls", Src: ap("192.0.2.10:40001"), Dst: ap("198.51.100.2:5061"), Raw: []byte(long)},
		{At: at.Add(20 * time.Millisecond), Transport: "tls", Src: ap("198.51.100.2:5061"), Dst: ap("192.0.2.10:40001"), Raw: []byte("SIP/2.0 200 OK\r\nCall-ID: c1\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n")},
		{At: at.Add(21 * time.Millisecond), Transport: "tcp", Src: ap("192.0.2.10:40001"), Dst: ap("198.51.100.2:5061"), Raw: []byte("ACK sip:1@198.51.100.2 SIP/2.0\r\nCall-ID: c1\r\nCSeq: 1 ACK\r\nContent-Length: 0\r\n\r\n")},
		{At: at.Add(30 * time.Millisecond), Transport: "udp", Src: ap("[2001:db8::10]:5060"), Dst: ap("[2001:db8::1]:5060"), Raw: []byte("SIP/2.0 200 OK\r\nCall-ID: c1\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n")},
	}
}

// TestPcap reads the file back frame by frame: lengths, checksums and
// sequence numbers must be what a capture of these messages would show.
func TestPcap(t *testing.T) {
	msgs := sample()
	var buf bytes.Buffer
	if err := WritePcap(&buf, msgs); err != nil {
		t.Fatal(err)
	}
	file := buf.Bytes()
	if binary.LittleEndian.Uint32(file) != 0xa1b2c3d4 || binary.LittleEndian.Uint32(file[20:]) != 1 {
		t.Fatal("not a pcap file with Ethernet frames")
	}
	type key struct{ src, dst string }
	streams := map[key][]byte{}
	seqs := map[key]uint32{}
	frames := 0
	for rest := file[24:]; len(rest) > 0; frames++ {
		n := int(binary.LittleEndian.Uint32(rest[8:]))
		if n != int(binary.LittleEndian.Uint32(rest[12:])) || 16+n > len(rest) {
			t.Fatalf("frame %d: bad lengths", frames)
		}
		frame := rest[16 : 16+n]
		rest = rest[16+n:]
		var proto byte
		var seg, addrs []byte
		switch binary.BigEndian.Uint16(frame[12:]) {
		case 0x0800:
			ip := frame[14:34]
			if fold(sum(0, ip)) != 0xffff || int(binary.BigEndian.Uint16(ip[2:])) != len(frame)-14 {
				t.Fatalf("frame %d: IPv4 header checksum or length", frames)
			}
			proto, addrs, seg = ip[9], ip[12:20], frame[34:]
		case 0x86dd:
			ip := frame[14:54]
			if int(binary.BigEndian.Uint16(ip[4:])) != len(frame)-54 {
				t.Fatalf("frame %d: IPv6 payload length", frames)
			}
			proto, addrs, seg = ip[6], ip[8:40], frame[54:]
		default:
			t.Fatalf("frame %d: ethertype", frames)
		}
		if fold(sum(sum(uint32(proto)+uint32(len(seg)), addrs), seg)) != 0xffff {
			t.Fatalf("frame %d: transport checksum", frames)
		}
		k := key{string(addrs[:len(addrs)/2]) + string(seg[0:2]), string(addrs[len(addrs)/2:]) + string(seg[2:4])}
		if proto == 17 {
			streams[k] = append(streams[k], seg[8:]...)
			continue
		}
		if seqs[k] == 0 {
			seqs[k] = 1
		}
		if got := binary.BigEndian.Uint32(seg[4:]); got != seqs[k] {
			t.Fatalf("frame %d: sequence %d, want %d", frames, got, seqs[k])
		}
		if len(seg)-20 > tcpSegmentSize {
			t.Fatalf("frame %d: segment of %d bytes", frames, len(seg)-20)
		}
		seqs[k] += uint32(len(seg) - 20)
		streams[k] = append(streams[k], seg[20:]...)
	}
	if frames != 7 { // the long message takes three segments
		t.Fatalf("%d frames, want 7", frames)
	}
	want := map[key][]byte{}
	for _, m := range msgs {
		k := key{string(m.Src.Addr().AsSlice()) + string(binary.BigEndian.AppendUint16(nil, m.Src.Port())),
			string(m.Dst.Addr().AsSlice()) + string(binary.BigEndian.AppendUint16(nil, m.Dst.Port()))}
		want[k] = append(want[k], m.Raw...)
	}
	for k, w := range want {
		if !bytes.Equal(streams[k], w) {
			t.Fatalf("stream %x → %x differs", k.src, k.dst)
		}
	}
}

// TestPcapReadByTcpdump hands the file to tcpdump, which must read every
// frame without a complaint and see the SIP text in it.
func TestPcapReadByTcpdump(t *testing.T) {
	tool, err := exec.LookPath("tcpdump")
	if err != nil {
		if tool, err = exec.LookPath("tshark"); err != nil {
			t.Skip("neither tcpdump nor tshark is installed")
		}
	}
	path := filepath.Join(t.TempDir(), "call.pcap")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePcap(f, sample()); err != nil {
		t.Fatal(err)
	}
	f.Close()
	args := []string{"-r", path, "-nn", "-A", "-vv"}
	if strings.HasSuffix(tool, "tshark") {
		args = []string{"-r", path, "-V"}
	}
	out, err := exec.Command(tool, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", tool, err, out)
	}
	text := string(out)
	for _, bad := range []string{"truncated", "bad cksum", "bad udp cksum", "incorrect", "Malformed"} {
		if strings.Contains(text, bad) {
			t.Errorf("%s reports %q:\n%s", tool, bad, text)
		}
	}
	for _, want := range []string{"192.0.2.1.5060 > 192.0.2.10.5060", "INVITE sip:1@192.0.2.10 SIP/2.0", "198.51.100.2.5061 > 192.0.2.10.40001", "2001:db8::10.5060 > 2001:db8::1.5060"} {
		if strings.HasSuffix(tool, "tcpdump") && !strings.Contains(text, want) {
			t.Errorf("tcpdump does not show %q:\n%s", want, text)
		}
	}
	if n := strings.Count(text, "SIP/2.0"); n < 5 {
		t.Errorf("%s shows SIP %d times, want every message", tool, n)
	}
}

// fakeClickHouse answers the collector's statements and keeps its inserts.
type fakeClickHouse struct {
	mu      sync.Mutex
	inserts [][]byte
	fail    int // inserts to refuse first
}

func (f *fakeClickHouse) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	q := r.URL.Query().Get("query")
	if !strings.HasPrefix(q, "insert into sip_messages") {
		if strings.Contains(string(body), "create_table_query") {
			io.WriteString(w, "CREATE TABLE x TTL toDateTime(at) + toIntervalDay(7)")
		}
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail > 0 {
		f.fail--
		http.Error(w, "busy", 500)
		return
	}
	f.inserts = append(f.inserts, body)
}

func (f *fakeClickHouse) stored() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return bytes.Join(f.inserts, nil)
}

func TestCollector(t *testing.T) {
	fake := &fakeClickHouse{fail: 1}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	c, err := Listen(Config{Listen: "127.0.0.1:0", ClickHouse: srv.URL + "/db", Every: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	conn, err := net.Dial("udp", c.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	at := time.Now()
	// Sent from 127.0.0.1, so messages to that address were received by the
	// "SIP core" and messages from it were sent.
	conn.Write(hep(17, "192.0.2.1:5060", "127.0.0.1:5060", at, invite))
	conn.Write(hep(6, "127.0.0.1:5060", "192.0.2.9:5060", at, strings.Replace(invite, "Call-ID: test-1", "Call-ID: test-2", 1)))
	conn.Write([]byte("not HEP at all"))
	deadline := time.Now().Add(10 * time.Second)
	for c.stored.Value() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	got := string(fake.stored())
	if c.stored.Value() != 2 || c.received.Value() != 3 || c.droppedBad.Value() != 1 || c.storeErrors.Value() != 1 {
		t.Fatalf("stored %d, received %d, unreadable %d, store errors %d", c.stored.Value(), c.received.Value(), c.droppedBad.Value(), c.storeErrors.Value())
	}
	for _, want := range []string{"test-1@172.30.0.21", "test-2@172.30.0.21", "\x02in\x03udp\x09192.0.2.1", "\x03out\x03tcp\x09127.0.0.1", `response="***"`} {
		if !strings.Contains(got, want) {
			t.Errorf("stored rows lack %q", want)
		}
	}
	if strings.Contains(got, "0123456789abcdef") {
		t.Error("a digest response was stored")
	}
}
