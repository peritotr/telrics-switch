-- SPDX-License-Identifier: AGPL-3.0-only
-- Rates of exchange may follow the European Central Bank's daily reference
-- rates (T-20, ADR-0036). A rate is either typed in (manual) or follows the
-- feed (ecb); rows from before were typed in and stay so. Only the feed
-- writes a following rate; any other change to such a row makes it manual,
-- so that a rate somebody chose is never overwritten.
alter table exchange_rates
  add column source text not null default 'manual' check (source in ('manual', 'ecb')),
  add column published date;

-- The last table the feed published: units of each currency one euro buys,
-- kept as the decimal text it was published in (exact; no floating point).
-- New rows can follow the feed at once, without waiting for the next fetch.
create table exchange_rate_feed (
  currency  char(3) primary key check (currency ~ '^[A-Z]{3}$'),
  per_eur   text not null check (per_eur ~ '^[0-9]{1,12}(\.[0-9]{1,12})?$'),
  published date not null
);

-- One row: when the feed was last asked, answered and failed, and which
-- following rates it could not give. claimed_until keeps several API
-- processes from fetching at once.
create table exchange_rate_fetch (
  id            boolean primary key default true check (id),
  claimed_until timestamptz,
  fetched_at    timestamptz,
  published     date,
  failed_at     timestamptz,
  error         text not null default '',
  missing       text[] not null default '{}'
);
insert into exchange_rate_fetch default values;

-- The feed's writes say so for their transaction (set_config
-- 'softswitch.fx_feed'); every other change to a following rate makes it
-- manual.
create function exchange_rate_kept() returns trigger language plpgsql as $$
begin
  if old.source = 'ecb' and new.source = 'ecb' and coalesce(current_setting('softswitch.fx_feed', true), '') <> 'on' then
    new.source := 'manual';
    new.published := null;
  end if;
  if new.source = 'manual' then
    new.published := null;
  end if;
  return new;
end $$;
create trigger exchange_rates_kept before update on exchange_rates
  for each row execute function exchange_rate_kept();
