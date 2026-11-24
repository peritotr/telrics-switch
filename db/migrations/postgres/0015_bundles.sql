-- SPDX-License-Identifier: AGPL-3.0-only
-- Bundles of minutes: talk time a client gets free each period (a calendar
-- month in the time zone of the client's tariff) towards numbers that start
-- with one of the bundle's prefixes. What a call takes from the bundle is
-- not charged; once the bundle is used up calls cost what the tariff says.
create table bundles (
  id          bigserial primary key,
  client_id   bigint not null references clients(id) on delete cascade,
  name        text not null default '',
  prefixes    text[] not null check (cardinality(prefixes) between 1 and 200 and array_to_string(prefixes, '') ~ '^[0-9]*$'
                                     and array_position(prefixes, null::text) is null),
  seconds     int not null check (seconds > 0),  -- billed seconds per period
  enabled     boolean not null default true,
  created_at  timestamptz not null default now()
);
create index bundles_client on bundles (client_id);

-- What has been taken from a bundle in a period ("2026-11"). A period with
-- no row has used nothing: a new month starts from zero by itself.
create table bundle_usage (
  bundle_id  bigint not null references bundles(id) on delete cascade,
  period     text not null check (period ~ '^[0-9]{4}-[0-9]{2}$'),
  used_sec   bigint not null default 0 check (used_sec >= 0),
  primary key (bundle_id, period)
);

-- Seconds of the call paid for by a bundle; sell_billed_sec includes them,
-- sell_amount does not.
alter table cdrs
  add column bundle_id bigint references bundles(id) on delete set null,
  add column bundle_sec int not null default 0;
