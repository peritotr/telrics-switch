-- SPDX-License-Identifier: AGPL-3.0-only
-- Reports sent by e-mail on a schedule: the summary of the day, week or
-- month just ended, to a list of addresses. sent_through is the end of the
-- last period that went out, so that each goes out once.
create table report_schedules (
  id           bigserial primary key,
  reseller_id  bigint not null references resellers(id) on delete cascade,
  name         text not null check (name <> ''),
  group_by     text not null check (group_by in ('client', 'vendor', 'prefix', 'day')),
  period       text not null check (period in ('daily', 'weekly', 'monthly')),
  recipients   text not null check (recipients ~ '^[^@\s,]+@[^@\s,]+(\s*,\s*[^@\s,]+@[^@\s,]+)*$'),
  timezone     text not null default 'UTC' check (now() at time zone timezone is not null),
  send_hour    smallint not null default 8 check (send_hour between 0 and 23),
  enabled      boolean not null default true,
  sent_through timestamptz,
  created_at   timestamptz not null default now(),
  unique (reseller_id, name)
);
