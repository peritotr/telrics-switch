// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
)

// SweepBalanceAlerts opens an alert for every client whose balance is at or
// below its threshold and has none open, and closes the alerts of clients
// that are back above it or no longer have a threshold. It looks at all
// clients at once, so balances may change by any path without telling it.
func (s *Server) SweepBalanceAlerts(ctx context.Context) (raised, cleared int64, err error) {
	// The client hears of a new alert by e-mail, if it has an address.
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			with raised as (
			  insert into balance_alerts (client_id, threshold, balance)
			  select id, low_balance, balance from clients where low_balance is not null and balance <= low_balance
			  on conflict (client_id) where cleared_at is null do nothing
			  returning client_id, threshold, balance)
			select c.id, c.name, coalesce(c.email, ''), c.currency, r.threshold, r.balance from raised r join clients c on c.id = r.client_id`)
		if err != nil {
			return err
		}
		type alert struct {
			id                    int64
			name, email, currency string
			threshold, balance    int64
		}
		var alerts []alert
		for rows.Next() {
			var a alert
			if err := rows.Scan(&a.id, &a.name, &a.email, &a.currency, &a.threshold, &a.balance); err != nil {
				return err
			}
			alerts = append(alerts, a)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		raised = int64(len(alerts))
		for _, a := range alerts {
			wanted := true
			for _, f := range s.alertMail {
				if wanted, err = f(ctx, tx, a.id); err != nil {
					return err
				} else if !wanted {
					break
				}
			}
			if !wanted {
				continue
			}
			if err := s.QueueMail(ctx, tx, a.email, "Düşük bakiye uyarısı", fmt.Sprintf(
				"Merhaba,\n\n%s hesabının bakiyesi belirlediğiniz eşiğe indi.\n\nBakiye: %s %s\nEşik: %s %s\n\nBakiye tükendiğinde çağrılarınız reddedilir. Kesinti yaşamamak için ödeme yapmanızı öneririz.\n",
				a.name, Amount(a.balance), a.currency, Amount(a.threshold), a.currency)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	tag, err := s.db.Exec(ctx, `
		update balance_alerts a set cleared_at = now() from clients c
		where a.cleared_at is null and c.id = a.client_id and (c.low_balance is null or c.balance > c.low_balance)`)
	if err != nil {
		return raised, 0, err
	}
	return raised, tag.RowsAffected(), nil
}

// OnAlertMail registers a say in whether a client is mailed about a new low
// balance alert. The alert itself is raised either way.
func (s *Server) OnAlertMail(f func(ctx context.Context, tx pgx.Tx, clientID int64) (bool, error)) {
	s.alertMail = append(s.alertMail, f)
}

// RunBalanceAlerts sweeps every interval until ctx ends.
func (s *Server) RunBalanceAlerts(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, _, err := s.SweepBalanceAlerts(ctx); err != nil && ctx.Err() == nil {
			log.Printf("api: balance alerts: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
