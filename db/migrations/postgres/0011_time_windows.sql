-- SPDX-License-Identifier: AGPL-3.0-only
-- Rates and routes may be limited to days of the week and hours of the day,
-- read in the time zone of their tariff or plan. days is a set of bits,
-- Monday = 1 up to Sunday = 64; time_from and time_to are minutes since
-- midnight, the window ending just before time_to. Equal values mean the
-- whole day; time_from after time_to is a window across midnight, counted
-- to the day it starts on.
alter table tariffs add column timezone text not null default 'UTC'
  check (now() at time zone timezone is not null);
alter table routing_plans add column timezone text not null default 'UTC'
  check (now() at time zone timezone is not null);

alter table rates
  add column days smallint not null default 127 check (days between 1 and 127),
  add column time_from smallint not null default 0 check (time_from between 0 and 1439),
  add column time_to smallint not null default 0 check (time_to between 0 and 1439);
alter table rates drop constraint rates_tariff_id_prefix_effective_from_key;
alter table rates add constraint rates_identity_key unique (tariff_id, prefix, effective_from, days, time_from, time_to);

alter table routing_entries
  add column days smallint not null default 127 check (days between 1 and 127),
  add column time_from smallint not null default 0 check (time_from between 0 and 1439),
  add column time_to smallint not null default 0 check (time_to between 0 and 1439);
