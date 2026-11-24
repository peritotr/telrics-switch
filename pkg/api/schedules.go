// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Scheduled reports: the summary of the day, week or month just ended, sent
// by e-mail. A schedule is due once its period is over and the hour it
// asks for has come, in its own time zone, and each period is sent once:
// the message is queued in the transaction that records the period as sent.

// reportPeriod gives the last complete period of a kind as of now, in loc:
// yesterday, last Monday-to-Sunday week, or last calendar month.
func reportPeriod(kind string, now time.Time, loc *time.Location) (start, end time.Time) {
	local := now.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	switch kind {
	case "weekly":
		end = day.AddDate(0, 0, -(int(day.Weekday())+6)%7) // back to Monday
		return end.AddDate(0, 0, -7), end
	case "monthly":
		end = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
		return end.AddDate(0, -1, 0), end
	default:
		return day.AddDate(0, 0, -1), day
	}
}

type reportSchedule struct {
	id, resellerID                          int64
	name, groupBy, period, recipients, zone string
	hour                                    int
	sentThrough                             *time.Time
}

// scheduleSent is what sending a report now did: the period it covers and
// the number of recipients it was queued for.
type scheduleSent struct {
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	Recipients  int       `json:"recipients"`
}

var (
	periodNames = map[string]string{"daily": "Günlük", "weekly": "Haftalık", "monthly": "Aylık"}
	groupNames  = map[string]string{"client": "Müşteri", "vendor": "Sağlayıcı", "prefix": "Önek", "day": "Gün"}
)

// reportMail writes the report of a schedule for a period as the subject
// and plain-text body of a message.
func (s *Server) reportMail(ctx context.Context, q pgx.Tx, sch reportSchedule, start, end time.Time) (subject, body string, err error) {
	raw, err := s.summary(ctx, Caller{ResellerID: sch.resellerID}, sch.groupBy, start, end)
	if err != nil {
		return "", "", err
	}
	var rows []summaryRow
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return "", "", err
	}
	// Names instead of ids where the rows are parties.
	names := map[string]string{}
	if table := map[string]string{"client": "clients", "vendor": "vendors"}[sch.groupBy]; table != "" {
		found, err := q.Query(ctx, `select id::text, name from `+table+` where reseller_id = $1`, sch.resellerID)
		if err != nil {
			return "", "", err
		}
		var id, name string
		if _, err := pgx.ForEachRow(found, []any{&id, &name}, func() error { names[id] = name; return nil }); err != nil {
			return "", "", err
		}
	}
	var currency string
	if err := q.QueryRow(ctx, `select currency from resellers where id = $1`, sch.resellerID).Scan(&currency); err != nil {
		return "", "", err
	}
	if sch.groupBy != "day" {
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Revenue > rows[j].Revenue })
	}
	pct := func(part, whole int64) string {
		if whole == 0 {
			return "-"
		}
		return strings.Replace(fmt.Sprintf("%.1f", float64(part)*100/float64(whole)), ".", ",", 1) // a ratio for reading, not money
	}
	line := func(r summaryRow, label string) string {
		return fmt.Sprintf("%-28.28s %9d %6s %6s %10d %14s %14s %14s\n", label, r.Calls, pct(r.Answered, r.Calls), pct(r.Calls-r.FailedNetwork, r.Calls),
			(r.DurationSec+59)/60, Amount(r.Revenue), Amount(r.Cost), Amount(r.Revenue-r.Cost))
	}
	const shown = 30
	var b strings.Builder
	last := end.Add(-time.Second)
	span := start.Format("02.01.2006")
	if last.Format("02.01.2006") != span {
		span += " - " + last.Format("02.01.2006")
	}
	fmt.Fprintf(&b, "%s rapor: %s\nDönem: %s (%s)\nTutarlar %s cinsindendir.\n\n", periodNames[sch.period], sch.name, span, sch.zone, currency)
	if len(rows) == 0 {
		b.WriteString("Bu dönemde çağrı yok.\n")
	} else {
		fmt.Fprintf(&b, "%-28s %9s %6s %6s %10s %14s %14s %14s\n", groupNames[sch.groupBy], "Çağrı", "ASR %", "NER %", "Dakika", "Gelir", "Maliyet", "Kâr")
		var total summaryRow
		for i, r := range rows {
			total.Calls += r.Calls
			total.Answered += r.Answered
			total.DurationSec += r.DurationSec
			total.FailedNetwork += r.FailedNetwork
			total.Revenue += r.Revenue
			total.Cost += r.Cost
			if i < shown {
				label := r.Key
				if n, ok := names[r.Key]; ok {
					label = n
				} else if label == "" {
					label = "(yok)"
				}
				b.WriteString(line(r, label))
			}
		}
		if len(rows) > shown {
			fmt.Fprintf(&b, "… ve %d satır daha (tamamı panelde)\n", len(rows)-shown)
		}
		b.WriteString(line(total, "Toplam"))
	}
	if s.panelURL != "" {
		fmt.Fprintf(&b, "\nAyrıntı: %s\n", s.panelURL)
	}
	return fmt.Sprintf("%s rapor: %s (%s)", periodNames[sch.period], sch.name, span), b.String(), nil
}

// queueReport queues a schedule's report for a period to every recipient.
func (s *Server) queueReport(ctx context.Context, tx pgx.Tx, sch reportSchedule, start, end time.Time) (int, error) {
	subject, body, err := s.reportMail(ctx, tx, sch, start, end)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, to := range strings.Split(sch.recipients, ",") {
		if to = strings.TrimSpace(to); to != "" {
			if err := s.QueueMail(ctx, tx, to, subject, body); err != nil {
				return 0, err
			}
			n++
		}
	}
	return n, nil
}

const scheduleCols = `id, reseller_id, name, group_by, period, recipients, timezone, send_hour, sent_through`

func scanSchedule(row pgx.Row) (sch reportSchedule, err error) {
	err = row.Scan(&sch.id, &sch.resellerID, &sch.name, &sch.groupBy, &sch.period, &sch.recipients, &sch.zone, &sch.hour, &sch.sentThrough)
	return sch, err
}

// SweepReportSchedules sends every report that is due and reports how many
// went out. Several API processes may run it side by side.
func (s *Server) SweepReportSchedules(ctx context.Context, now time.Time) (sent int, err error) {
	if !s.MailEnabled() {
		return 0, nil
	}
	rows, err := s.db.Query(ctx, `select id from report_schedules where enabled order by id`)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
			sch, err := scanSchedule(tx.QueryRow(ctx, `select `+scheduleCols+` from report_schedules where id = $1 and enabled for update skip locked`, id))
			if err == pgx.ErrNoRows {
				return nil // gone, switched off, or another process has it
			}
			if err != nil {
				return err
			}
			loc, err := time.LoadLocation(sch.zone)
			if err != nil {
				loc = time.UTC
			}
			start, end := reportPeriod(sch.period, now, loc)
			if now.Before(end.Add(time.Duration(sch.hour)*time.Hour)) || sch.sentThrough != nil && !sch.sentThrough.Before(end) {
				return nil
			}
			if _, err := s.queueReport(ctx, tx, sch, start, end); err != nil {
				return err
			}
			sent++
			_, err = tx.Exec(ctx, `update report_schedules set sent_through = $2 where id = $1`, id, end)
			return err
		})
		if err != nil {
			return sent, fmt.Errorf("schedule %d: %w", id, err)
		}
	}
	return sent, nil
}

// RunReportSchedules sends due reports until ctx ends.
func (s *Server) RunReportSchedules(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, err := s.SweepReportSchedules(ctx, time.Now()); err != nil && ctx.Err() == nil {
			log.Printf("api: scheduled reports: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// sendScheduleNow queues a schedule's report for its last complete period
// at once, whatever was sent before; the schedule itself is not touched.
func (s *Server) sendScheduleNow(w http.ResponseWriter, r *http.Request, c Caller) error {
	id, err := PathID(r)
	if err != nil {
		return err
	}
	if !s.MailEnabled() {
		return Errf(409, "no mail server is configured")
	}
	ctx := r.Context()
	if err := s.Resource("report-schedules").Allowed(ctx, s.db, c, id, true); err != nil {
		return err
	}
	var out scheduleSent
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		sch, err := scanSchedule(tx.QueryRow(ctx, `select `+scheduleCols+` from report_schedules where id = $1`, id))
		if err != nil {
			return err
		}
		loc, err := time.LoadLocation(sch.zone)
		if err != nil {
			loc = time.UTC
		}
		start, end := reportPeriod(sch.period, time.Now(), loc)
		n, err := s.queueReport(ctx, tx, sch, start, end)
		out = scheduleSent{PeriodStart: start, PeriodEnd: end, Recipients: n}
		if err != nil {
			return err
		}
		return s.Audit(ctx, tx, r, c, AuditEntry{Resource: "report-schedules", RowID: id, Action: "send", Label: sch.name,
			Detail: map[string]any{"period_start": start, "period_end": end, "recipients": n}})
	})
	if err != nil {
		return err
	}
	WriteJSON(w, 200, out)
	return nil
}
