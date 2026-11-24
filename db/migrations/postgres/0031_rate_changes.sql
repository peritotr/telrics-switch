-- SPDX-License-Identifier: AGPL-3.0-only
-- Which rates changed, so that a snapshot load reads only those again
-- (T-30, ADR-0048). Every statement that writes rates notes the tariff and
-- prefix of each row it touched, with the writing transaction's id: a load
-- remembers the database snapshot it read under and next time asks for the
-- notes of transactions that snapshot did not see, whatever order they
-- committed in. A statement of more than 10,000 rows notes the tariff alone
-- (prefix null: read all of it again). A trigger rather than the API, so that
-- writes from anywhere count. Notes older than an hour are dropped; a load
-- whose previous one is older than half that reads everything.
create table rate_changes (
  xid       xid8 not null default pg_current_xact_id(),
  at        timestamptz not null default now(),
  tariff_id bigint not null,
  prefix    text
);
create index rate_changes_at on rate_changes (at);

create function rates_note() returns trigger language plpgsql as $$
declare
  big boolean;
begin
  delete from rate_changes where at < now() - interval '1 hour';
  if tg_op = 'TRUNCATE' then
    insert into rate_changes (tariff_id) select id from tariffs;
  elsif tg_op = 'INSERT' then
    big := (select count(*) from new_rows) > 10000;
    insert into rate_changes (tariff_id, prefix)
      select distinct tariff_id, case when big then null else prefix end from new_rows;
  elsif tg_op = 'DELETE' then
    big := (select count(*) from old_rows) > 10000;
    insert into rate_changes (tariff_id, prefix)
      select distinct tariff_id, case when big then null else prefix end from old_rows;
  else
    big := (select count(*) from new_rows) > 10000;
    insert into rate_changes (tariff_id, prefix)
      select distinct tariff_id, case when big then null else prefix end
      from (select tariff_id, prefix from new_rows union all select tariff_id, prefix from old_rows) r;
  end if;
  return null;
end $$;

-- A trigger with transition tables takes one event each.
create trigger rates_note_insert after insert on rates referencing new table as new_rows
  for each statement execute function rates_note();
create trigger rates_note_update after update on rates referencing old table as old_rows new table as new_rows
  for each statement execute function rates_note();
create trigger rates_note_delete after delete on rates referencing old table as old_rows
  for each statement execute function rates_note();
create trigger rates_note_truncate after truncate on rates
  for each statement execute function rates_note();

-- When the next rate of a tariff comes into force, asked on every read of a
-- tariff's rates.
create index rates_tariff_effective_from on rates (tariff_id, effective_from);
