-- SPDX-License-Identifier: AGPL-3.0-only
-- A trunk known by its address may have several: single addresses and
-- networks (CIDR), IPv4 and IPv6. They replace the single "ip" column.
alter table client_trunks add column addresses inet[] not null default '{}';

-- Addresses are kept tidy: bits past the length of a network are dropped
-- (10.0.0.9/24 is 10.0.0.0/24), the list is sorted and holds nothing twice.
-- A network so wide that it would make most of the Internet a known source
-- is refused.
create function client_trunk_addresses_tidy() returns trigger language plpgsql as $$
begin
  if new.addresses is null or array_ndims(new.addresses) > 1
     or exists (select 1 from unnest(new.addresses) a where a is null) then
    raise exception 'addresses must be a list of addresses and networks' using errcode = '22004';
  end if;
  if exists (select 1 from unnest(new.addresses) a where masklen(a) < case family(a) when 4 then 8 else 16 end) then
    raise exception 'a network may not be wider than /8 (IPv4) or /16 (IPv6)' using errcode = '22023';
  end if;
  new.addresses := array(
    select distinct case when masklen(a) = case family(a) when 4 then 32 else 128 end then a else network(a)::inet end
    from unnest(new.addresses) a order by 1);
  return new;
end $$;
create trigger client_trunk_addresses_tidy before insert or update of addresses on client_trunks
  for each row execute function client_trunk_addresses_tidy();

-- What the enabled trunks answer to, one row per address or network and
-- tech prefix: kept by trigger so that no two enabled trunks can claim the
-- same pair. Nothing else writes or reads it.
create table client_trunk_sources (
  addr        inet not null,
  tech_prefix text not null,
  trunk_id    bigint not null references client_trunks(id) on delete cascade,
  constraint client_trunk_address_in_use primary key (addr, tech_prefix)
);
create index client_trunk_sources_trunk on client_trunk_sources (trunk_id);

create function client_trunk_sources_sync() returns trigger language plpgsql as $$
begin
  if tg_op = 'UPDATE' then
    delete from client_trunk_sources where trunk_id = new.id;
  end if;
  if new.enabled and new.auth_type = 'ip' then
    insert into client_trunk_sources (addr, tech_prefix, trunk_id)
      select a, new.tech_prefix, new.id from unnest(new.addresses) a;
  end if;
  return null;
end $$;
create trigger client_trunk_sources_sync after insert or update of addresses, tech_prefix, enabled, auth_type on client_trunks
  for each row execute function client_trunk_sources_sync();

-- Existing trunks keep their one address. Only the host part ever counted.
update client_trunks set addresses = array[host(ip)::inet] where ip is not null;

-- Dropping the column takes its unique index and the old check with it.
alter table client_trunks drop column ip;
alter table client_trunks add constraint client_trunks_identity
  check ((auth_type = 'ip' and cardinality(addresses) > 0) or (auth_type = 'digest' and username is not null));
