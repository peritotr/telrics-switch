-- SPDX-License-Identifier: AGPL-3.0-only
-- Low balance alerts: a client may name a balance at or below which an
-- alert is raised. Null means no alert.
alter table clients add column low_balance bigint;

-- One row per time a client's balance stayed at or below its threshold.
create table balance_alerts (
  id         bigserial primary key,
  client_id  bigint not null references clients(id) on delete cascade,
  threshold  bigint not null,            -- as it was when the alert was raised
  balance    bigint not null,            -- as it was when the alert was raised
  raised_at  timestamptz not null default now(),
  cleared_at timestamptz                 -- null while the alert is open
);
create unique index balance_alerts_open on balance_alerts (client_id) where cleared_at is null;
create index balance_alerts_time on balance_alerts (raised_at desc, id desc);
