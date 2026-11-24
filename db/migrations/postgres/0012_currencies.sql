-- SPDX-License-Identifier: AGPL-3.0-only
-- Several currencies. An account keeps its books in its own currency
-- (resellers.currency); exchange_rates says what one unit of another
-- currency is worth in it, in micro-units. A client or vendor holds its
-- balance in its own currency and its tariff is in that currency too, so a
-- price never needs converting to be charged; rates of exchange serve to
-- compare vendors with each other and with the selling price, and to add
-- up reports.
create table exchange_rates (
  id          bigserial primary key,
  reseller_id bigint not null references resellers(id) on delete cascade,
  currency    char(3) not null check (currency ~ '^[A-Z]{3}$'),
  rate        bigint not null check (rate > 0),
  updated_at  timestamptz not null default now(),
  unique (reseller_id, currency)
);

-- What each leg came to in the account's currency when it was rated. Null
-- on records from before: those count at their own amounts.
alter table cdrs add column sell_base bigint, add column buy_base bigint;

-- A party's currency must be the account's or one with a rate of exchange,
-- and its tariff must be in the same currency.
create function party_currency_check() returns trigger language plpgsql as $$
declare
  base char(3) := (select currency from resellers where id = new.reseller_id);
  tariff char(3) := (select currency from tariffs where id = new.tariff_id);
begin
  if new.currency <> base and not exists (select 1 from exchange_rates where reseller_id = new.reseller_id and currency = new.currency) then
    raise exception 'no exchange rate for %: add one before using the currency', new.currency using errcode = '23514';
  end if;
  if tariff is not null and tariff <> new.currency then
    raise exception 'the tariff is in %, the account in %: they must be the same', tariff, new.currency using errcode = '23514';
  end if;
  return new;
end $$;
create trigger clients_currency before insert or update of currency, tariff_id on clients
  for each row execute function party_currency_check();
create trigger vendors_currency before insert or update of currency, tariff_id on vendors
  for each row execute function party_currency_check();

-- A tariff in use keeps its currency; a rate of exchange in use stays.
create function tariff_currency_check() returns trigger language plpgsql as $$
begin
  if exists (select 1 from clients where tariff_id = new.id and currency <> new.currency)
     or exists (select 1 from vendors where tariff_id = new.id and currency <> new.currency) then
    raise exception 'the tariff is used by accounts in %', old.currency using errcode = '23514';
  end if;
  return new;
end $$;
create trigger tariffs_currency before update of currency on tariffs
  for each row execute function tariff_currency_check();

create function exchange_rate_in_use() returns trigger language plpgsql as $$
begin
  if exists (select 1 from clients where reseller_id = old.reseller_id and currency = old.currency)
     or exists (select 1 from vendors where reseller_id = old.reseller_id and currency = old.currency) then
    raise exception 'accounts in % still need this rate', old.currency using errcode = '23503';
  end if;
  return old;
end $$;
create trigger exchange_rates_in_use before delete on exchange_rates
  for each row execute function exchange_rate_in_use();

create function exchange_rate_touched() returns trigger language plpgsql as $$
begin
  new.updated_at := now();
  return new;
end $$;
create trigger exchange_rates_touched before update on exchange_rates
  for each row execute function exchange_rate_touched();
