// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"fmt"
	"log"
	"mime"
	"net/http"
	"strings"

	"softswitch/core/pkg/trace"
)

// A call's signalling, as the trace collector stored it in ClickHouse: the
// messages in order, and the same messages as a capture file. Who may see
// a call's record may see its trace.

// traceLimit bounds one answer; a call has a few dozen messages.
const traceLimit = 2000

func (s *Server) registerTrace() {
	s.Handle("GET /v1/trace", false, s.callTrace,
		Summary("A call's SIP messages in order, every leg, from the stored signalling."),
		Query("call_id", "", true, "The call."), Returns(200, callTrace{}))
	s.Handle("GET /v1/trace/pcap", false, s.callTracePcap,
		Summary("The same messages as a capture file for Wireshark."),
		Query("call_id", "", true, "The call."),
		ReturnsText("application/vnd.tcpdump.pcap", "The messages with made-up Ethernet, IP and UDP or TCP headers."))
}

// traceMessage is one SIP message of a call; Line is its first line, Raw
// all of it, with the Authorization headers masked.
type traceMessage struct {
	At        string `json:"at"`        // RFC 3339 in microseconds
	Direction string `json:"direction"` // in (received by the SIP core), out (sent by it) or "" (unknown)
	Transport string `json:"transport"`
	SrcIP     string `json:"src_ip"`
	SrcPort   uint16 `json:"src_port"`
	DstIP     string `json:"dst_ip"`
	DstPort   uint16 `json:"dst_port"`
	Line      string `json:"line"`
	Raw       string `json:"raw"`
}

// callTrace is a call's messages; Truncated says there were more than one
// answer holds.
type callTrace struct {
	CallID    string         `json:"call_id"`
	Messages  []traceMessage `json:"messages"`
	Truncated bool           `json:"truncated"`
}

// traceFor checks the caller's right to the call and reads its messages.
func (s *Server) traceFor(r *http.Request, c Caller) (string, []trace.Message, error) {
	id := r.URL.Query().Get("call_id")
	if id == "" || len(id) > 512 {
		return "", nil, Errf(400, "call_id is required")
	}
	if s.analytics == nil {
		return "", nil, Errf(503, "call traces are not stored: no ClickHouse is configured")
	}
	if err := s.mayTrace(r.Context(), c, id); err != nil {
		return "", nil, err
	}
	msgs, err := trace.Query(r.Context(), s.analytics, id, traceLimit+1)
	if err != nil {
		log.Printf("api: call trace: %v", err)
		return "", nil, Errf(503, "call traces cannot be read at the moment")
	}
	return id, msgs, nil
}

// mayTrace lets a caller see the trace of a call that has a record among
// its clients. A call without a record yet (one in progress, say) belongs
// to nobody in particular: only an account that sees every client gets it.
// Anything else is "not found", whether or not the call exists.
func (s *Server) mayTrace(ctx context.Context, c Caller, callID string) error {
	var own, all bool
	var ids string
	err := s.db.QueryRow(ctx, fmt.Sprintf(`
		select exists (select 1 from cdrs r join clients t on t.id = r.client_id where r.call_id = $2 and t.reseller_id in %[1]s),
		       not exists (select 1 from clients t where not (t.reseller_id in %[1]s)),
		       (select coalesce(string_agg(id::text, ','), '') from clients t where t.reseller_id in %[1]s)`, s.scope),
		c.ResellerID, callID).Scan(&own, &all, &ids)
	if err != nil || own || all {
		return err
	}
	// The record may have left PostgreSQL for the archive.
	if ids != "" {
		out, err := s.analytics.Query(ctx, "select count() from cdrs where call_id = {call_id:String} and client_id in ("+ids+") format TSV",
			map[string]string{"call_id": callID})
		if err == nil && strings.TrimSpace(string(out)) != "0" {
			return nil
		}
	}
	return Errf(404, "not found")
}

func (s *Server) callTrace(w http.ResponseWriter, r *http.Request, c Caller) error {
	id, msgs, err := s.traceFor(r, c)
	if err != nil {
		return err
	}
	out := callTrace{CallID: id, Messages: []traceMessage{}, Truncated: len(msgs) > traceLimit}
	for _, m := range msgs[:min(len(msgs), traceLimit)] {
		out.Messages = append(out.Messages, traceMessage{
			At: m.At.Format("2006-01-02T15:04:05.000000Z"), Direction: m.Direction, Transport: m.Transport,
			SrcIP: m.Src.Addr().String(), SrcPort: m.Src.Port(), DstIP: m.Dst.Addr().String(), DstPort: m.Dst.Port(),
			Line: strings.ToValidUTF8(trace.FirstLine(m.Raw), "�"), Raw: strings.ToValidUTF8(string(m.Raw), "�"),
		})
	}
	WriteJSON(w, 200, out)
	return nil
}

func (s *Server) callTracePcap(w http.ResponseWriter, r *http.Request, c Caller) error {
	id, msgs, err := s.traceFor(r, c)
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		return Errf(404, "no trace is stored for this call")
	}
	name := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '.' || r == '_' {
			return r
		}
		return '_'
	}, id)
	w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name[:min(len(name), 80)] + ".pcap"}))
	return trace.WritePcap(w, msgs[:min(len(msgs), traceLimit)])
}
