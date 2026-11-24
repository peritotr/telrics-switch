// SPDX-License-Identifier: AGPL-3.0-only

package trace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"softswitch/core/pkg/clickhouse"
)

const columns = "at, seq, call_id, direction, transport, src_ip, src_port, dst_ip, dst_port, raw"

// encode appends a message as a row in ClickHouse's RowBinary format, which
// carries the message's bytes as they are.
func encode(b []byte, m Message) []byte {
	str := func(s string) {
		b = binary.AppendUvarint(b, uint64(len(s)))
		b = append(b, s...)
	}
	b = binary.LittleEndian.AppendUint64(b, uint64(m.At.UnixMicro()))
	b = binary.LittleEndian.AppendUint64(b, m.Seq)
	str(m.CallID)
	str(m.Direction)
	str(m.Transport)
	str(m.Src.Addr().String())
	b = binary.LittleEndian.AppendUint16(b, m.Src.Port())
	str(m.Dst.Addr().String())
	b = binary.LittleEndian.AppendUint16(b, m.Dst.Port())
	b = binary.AppendUvarint(b, uint64(len(m.Raw)))
	return append(b, m.Raw...)
}

// Insert stores messages.
func Insert(ctx context.Context, ch *clickhouse.Client, msgs []Message) error {
	var body []byte
	for _, m := range msgs {
		body = encode(body, m)
	}
	return ch.Exec(ctx, "insert into sip_messages ("+columns+") format RowBinary", bytes.NewReader(body))
}

// A query parameter is read the way a tab-separated value is.
var paramEscape = strings.NewReplacer(`\\`, `\\\\`, "\t", `\t`, "\n", `\n`)

// Query returns the messages of a call in the order they crossed the SIP
// core, at most limit of them.
func Query(ctx context.Context, ch *clickhouse.Client, callID string, limit int) ([]Message, error) {
	out, err := ch.Query(ctx, fmt.Sprintf(`
		select toUnixTimestamp64Micro(at) as at, seq, direction, transport, src_ip, src_port, dst_ip, dst_port, base64Encode(raw) as raw
		from sip_messages where call_id = {call_id:String} order by at, seq limit %d
		format JSONEachRow settings output_format_json_quote_64bit_integers = 0`, limit), map[string]string{"call_id": paramEscape.Replace(callID)})
	if err != nil {
		return nil, err
	}
	var msgs []Message
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var row struct {
			At        int64  `json:"at"`
			Seq       uint64 `json:"seq"`
			Direction string `json:"direction"`
			Transport string `json:"transport"`
			SrcIP     string `json:"src_ip"`
			SrcPort   uint16 `json:"src_port"`
			DstIP     string `json:"dst_ip"`
			DstPort   uint16 `json:"dst_port"`
			Raw       string `json:"raw"`
		}
		if err := dec.Decode(&row); err != nil {
			return nil, err
		}
		m := Message{At: time.UnixMicro(row.At).UTC(), Seq: row.Seq, CallID: callID, Direction: row.Direction, Transport: row.Transport}
		if a, err := netip.ParseAddr(row.SrcIP); err == nil {
			m.Src = netip.AddrPortFrom(a, row.SrcPort)
		}
		if a, err := netip.ParseAddr(row.DstIP); err == nil {
			m.Dst = netip.AddrPortFrom(a, row.DstPort)
		}
		if m.Raw, err = base64.StdEncoding.DecodeString(row.Raw); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// DefaultKeepDays is how long messages are kept when nothing else is asked.
const DefaultKeepDays = 7

// SetKeep makes the table drop messages older than days. It changes the
// table only when it says something else, as a change makes ClickHouse go
// over everything stored.
func SetKeep(ctx context.Context, ch *clickhouse.Client, days int) error {
	out, err := ch.Query(ctx, `select create_table_query from system.tables where database = currentDatabase() and name = 'sip_messages' format TSVRaw`, nil)
	if err != nil {
		return err
	}
	want := fmt.Sprintf("toIntervalDay(%d)", days)
	if strings.Contains(string(out), "TTL toDateTime(at) + "+want) {
		return nil
	}
	return ch.Exec(ctx, fmt.Sprintf("alter table sip_messages modify ttl toDateTime(at) + interval %d day", days), nil)
}
