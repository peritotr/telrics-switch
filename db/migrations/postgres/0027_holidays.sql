-- SPDX-License-Identifier: AGPL-3.0-only
-- Public holidays (T-19, ADR-0044): days of the account's calendar that
-- count as another day of the week for the windows of rates and routes
-- (days, time_from, time_to). counts_as is that day, Monday = 1 up to
-- Sunday = 7; a holiday counts as a Sunday unless told otherwise. Whether a
-- moment falls on a holiday is read in the time zone of the tariff or plan
-- whose window is being read, as the window itself is. The calendar is the
-- one of the account that owns the tariff or plan. No row exists yet, so
-- every window keeps its behaviour.
create table holidays (
  id          bigserial primary key,
  reseller_id bigint not null references resellers(id) on delete cascade,
  day         date not null,
  counts_as   smallint not null default 7 check (counts_as between 1 and 7),
  name        text not null default '' check (length(name) <= 200),
  created_at  timestamptz not null default now(),
  unique (reseller_id, day)
);
