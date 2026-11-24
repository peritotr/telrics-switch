-- SPDX-License-Identifier: AGPL-3.0-only
-- A trunk's client may write numbers in more than one way (00… and +…, for
-- instance): the trunk keeps a list of number rules, each {"strip", "add"},
-- and a dialled number takes the rule with the longest strip it starts
-- with (T-17). An empty strip matches every number.
--
-- number_strip and number_add stay for API clients that still send them:
-- written alone they stand for a list of one rule (none when both are
-- empty); they always show the list when it has one rule and are empty
-- otherwise. The list is what decides.
alter table client_trunks add column number_rules jsonb not null default '[]';

-- Existing trunks keep their one rule, and with it their behaviour.
update client_trunks
  set number_rules = jsonb_build_array(jsonb_build_object('strip', number_strip, 'add', number_add))
  where number_strip <> '' or number_add <> '';

create function client_trunk_number_rules_tidy() returns trigger language plpgsql as $$
declare
  rule jsonb;
begin
  -- Which side was written: the list wins over the two old columns.
  if (tg_op = 'INSERT' and new.number_rules = '[]' and (new.number_strip <> '' or new.number_add <> ''))
     or (tg_op = 'UPDATE' and new.number_rules is not distinct from old.number_rules
         and (new.number_strip, new.number_add) is distinct from (old.number_strip, old.number_add)) then
    new.number_rules := case when new.number_strip = '' and new.number_add = '' then '[]'::jsonb
      else jsonb_build_array(jsonb_build_object('strip', new.number_strip, 'add', new.number_add)) end;
  end if;
  if new.number_rules is null or jsonb_typeof(new.number_rules) <> 'array' then
    raise exception 'number_rules must be a list of {"strip", "add"}' using errcode = '22023';
  end if;
  if jsonb_array_length(new.number_rules) > 20 then
    raise exception 'a trunk may have at most 20 number rules' using errcode = '22023';
  end if;
  for rule in select * from jsonb_array_elements(new.number_rules) loop
    if jsonb_typeof(rule) <> 'object' or exists (select 1 from jsonb_object_keys(rule) k where k not in ('strip', 'add'))
       or jsonb_typeof(coalesce(rule->'strip', '""')) <> 'string' or jsonb_typeof(coalesce(rule->'add', '""')) <> 'string' then
      raise exception 'number_rules must be a list of {"strip", "add"}' using errcode = '22023';
    end if;
    if coalesce(rule->>'strip', '') !~ '^\+?[0-9]{0,8}$' then
      raise exception 'number rule strip must be at most 8 digits, with an optional + in front' using errcode = '22023';
    end if;
    if coalesce(rule->>'add', '') !~ '^[0-9]{0,8}$' then
      raise exception 'number rule add must be at most 8 digits' using errcode = '22023';
    end if;
    if coalesce(rule->>'strip', '') = '' and coalesce(rule->>'add', '') = '' then
      raise exception 'a number rule must strip or add something' using errcode = '22023';
    end if;
  end loop;
  if (select count(distinct coalesce(e->>'strip', '')) from jsonb_array_elements(new.number_rules) e) <> jsonb_array_length(new.number_rules) then
    raise exception 'two number rules strip the same prefix' using errcode = '22023';
  end if;
  -- Kept in the order they are tried: longest strip first.
  new.number_rules := (select coalesce(jsonb_agg(jsonb_build_object('strip', s, 'add', a) order by length(s) desc, s), '[]')
    from (select coalesce(e->>'strip', '') s, coalesce(e->>'add', '') a from jsonb_array_elements(new.number_rules) e) r);
  if jsonb_array_length(new.number_rules) = 1 then
    new.number_strip := new.number_rules->0->>'strip';
    new.number_add := new.number_rules->0->>'add';
  else
    new.number_strip := '';
    new.number_add := '';
  end if;
  return new;
end $$;
create trigger client_trunk_number_rules_tidy before insert or update of number_rules, number_strip, number_add on client_trunks
  for each row execute function client_trunk_number_rules_tidy();
