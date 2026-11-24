-- SPDX-License-Identifier: AGPL-3.0-only
-- Routing data for the SIP lab: the SIPp caller (172.30.0.21) as a client and
-- the three SIPp answering sides as vendors. Safe to run more than once.
--   99…  three vendors, cheapest first: two refuse, the third answers
--   90…  straight to the answering vendor (used by the load test)

insert into resellers (name) select 'root' where not exists (select 1 from resellers);

do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  sell bigint; plan bigint; cl bigint; v bigint; t bigint; c bigint;
  spec record;
begin
  insert into tariffs (reseller_id, name, kind) values (root, 'Lab sell', 'sell') on conflict do nothing;
  select id into sell from tariffs where reseller_id = root and name = 'Lab sell';
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values
    (sell, '99', 20000, '2026-01-01'), (sell, '90', 20000, '2026-01-01') on conflict do nothing;

  insert into routing_plans (reseller_id, name, policy) values (root, 'Lab plan', 'lcr') on conflict do nothing;
  select id into plan from routing_plans where reseller_id = root and name = 'Lab plan';

  for spec in select * from (values
      ('Lab vendor A (500)', '172.30.0.22', 5000),
      ('Lab vendor B (503)', '172.30.0.23', 8000),
      ('Lab vendor C (answers)', '172.30.0.20', 12000)) as s(name, host, price)
  loop
    insert into tariffs (reseller_id, name, kind) values (root, spec.name, 'buy') on conflict do nothing;
    select id into t from tariffs where reseller_id = root and name = spec.name;
    insert into rates (tariff_id, prefix, price_per_min, effective_from) values (t, '99', spec.price, '2026-01-01') on conflict do nothing;
    insert into vendors (reseller_id, name, tariff_id) values (root, spec.name, t) on conflict do nothing;
    select id into v from vendors where reseller_id = root and name = spec.name;
    select id into c from vendor_connections where vendor_id = v and name = 'lab';
    if c is null then
      insert into vendor_connections (vendor_id, name, host) values (v, 'lab', spec.host) returning id into c;
    end if;
    insert into routing_entries (routing_plan_id, prefix, connection_id) values (plan, '99', c) on conflict do nothing;
    if spec.host = '172.30.0.20' then
      insert into rates (tariff_id, prefix, price_per_min, effective_from) values (t, '90', spec.price, '2026-01-01') on conflict do nothing;
      insert into routing_entries (routing_plan_id, prefix, connection_id) values (plan, '90', c) on conflict do nothing;
    end if;
  end loop;

  insert into clients (reseller_id, name, balance, tariff_id, routing_plan_id)
    values (root, 'Lab SIPp', 1000000000, sell, plan) on conflict do nothing;
  select id into cl from clients where reseller_id = root and name = 'Lab SIPp';
  if not exists (select 1 from client_trunks where client_id = cl) then
    insert into client_trunks (client_id, name, addresses) values (cl, 'sipp-uac', '{172.30.0.21}');
  end if;
end $$;

-- A second client reached with tech prefix 555 from the same address. Its
-- balance pays for exactly three seconds: the cut-off scenario resets it.
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  cl bigint;
begin
  insert into clients (reseller_id, name, balance, tariff_id, routing_plan_id)
    select root, 'Lab SIPp low balance', 1000, tariff_id, routing_plan_id from clients where reseller_id = root and name = 'Lab SIPp'
    on conflict do nothing;
  select id into cl from clients where reseller_id = root and name = 'Lab SIPp low balance';
  if not exists (select 1 from client_trunks where client_id = cl) then
    insert into client_trunks (client_id, name, addresses, tech_prefix) values (cl, 'sipp-uac-555', '{172.30.0.21}', '555');
  end if;
end $$;

-- A client that signs in with a user name and password instead of a known
-- address. Lab only: the password is "lab-only-Digest-2026", realm "softswitch".
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  cl bigint;
begin
  insert into clients (reseller_id, name, balance, tariff_id, routing_plan_id)
    select root, 'Lab SIPp digest', 1000000000, tariff_id, routing_plan_id from clients where reseller_id = root and name = 'Lab SIPp'
    on conflict do nothing;
  select id into cl from clients where reseller_id = root and name = 'Lab SIPp digest';
  if not exists (select 1 from client_trunks where client_id = cl) then
    insert into client_trunks (client_id, name, auth_type, username, password_ha1)
      values (cl, 'sipp-digest', 'digest', 'lab-digest', md5('lab-digest:softswitch:lab-only-Digest-2026'));
  end if;
end $$;

-- A vendor reached over TLS with encrypted media, for destinations 97….
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  sell bigint := (select id from tariffs where reseller_id = root and name = 'Lab sell');
  plan bigint := (select id from routing_plans where reseller_id = root and name = 'Lab plan');
  t bigint; v bigint; c bigint;
begin
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (sell, '97', 20000, '2026-01-01') on conflict do nothing;
  insert into tariffs (reseller_id, name, kind) values (root, 'Lab vendor D (TLS)', 'buy') on conflict do nothing;
  select id into t from tariffs where reseller_id = root and name = 'Lab vendor D (TLS)';
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (t, '97', 9000, '2026-01-01') on conflict do nothing;
  insert into vendors (reseller_id, name, tariff_id) values (root, 'Lab vendor D (TLS)', t) on conflict do nothing;
  select id into v from vendors where reseller_id = root and name = 'Lab vendor D (TLS)';
  select id into c from vendor_connections where vendor_id = v and name = 'lab';
  if c is null then
    insert into vendor_connections (vendor_id, name, host, port, transport) values (v, 'lab', '172.30.0.24', 5061, 'tls') returning id into c;
  end if;
  insert into routing_entries (routing_plan_id, prefix, connection_id) values (plan, '97', c) on conflict do nothing;
end $$;

-- A TLS vendor nobody listens at, cheaper than vendor D, for destinations
-- 96…: the connection is refused and the call must go on to vendor D.
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  sell bigint := (select id from tariffs where reseller_id = root and name = 'Lab sell');
  plan bigint := (select id from routing_plans where reseller_id = root and name = 'Lab plan');
  d_tariff bigint := (select id from tariffs where reseller_id = root and name = 'Lab vendor D (TLS)');
  d_conn bigint := (select vc.id from vendor_connections vc join vendors v on v.id = vc.vendor_id
                    where v.reseller_id = root and v.name = 'Lab vendor D (TLS)' and vc.name = 'lab');
  t bigint; v bigint; c bigint;
begin
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (sell, '96', 20000, '2026-01-01') on conflict do nothing;
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (d_tariff, '96', 9000, '2026-01-01') on conflict do nothing;
  insert into routing_entries (routing_plan_id, prefix, connection_id) values (plan, '96', d_conn) on conflict do nothing;
  insert into tariffs (reseller_id, name, kind) values (root, 'Lab vendor E (TLS, down)', 'buy') on conflict do nothing;
  select id into t from tariffs where reseller_id = root and name = 'Lab vendor E (TLS, down)';
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (t, '96', 5000, '2026-01-01') on conflict do nothing;
  insert into vendors (reseller_id, name, tariff_id) values (root, 'Lab vendor E (TLS, down)', t) on conflict do nothing;
  select id into v from vendors where reseller_id = root and name = 'Lab vendor E (TLS, down)';
  select id into c from vendor_connections where vendor_id = v and name = 'lab';
  if c is null then
    insert into vendor_connections (vendor_id, name, host, port, transport) values (v, 'lab', '172.30.0.24', 5099, 'tls') returning id into c;
  end if;
  insert into routing_entries (routing_plan_id, prefix, connection_id) values (plan, '96', c) on conflict do nothing;
end $$;

-- Trunks of the lab client with a rule about how calls arrive: prefix 881
-- only over TLS, prefix 882 only with the lab caller's certificate.
insert into client_trunks (client_id, name, addresses, tech_prefix, security, tls_cert_name)
  select id, 'sipp-uac-tls', '{172.30.0.21}', '881', 'tls', '' from clients where name = 'Lab SIPp'
  and not exists (select 1 from client_trunks where name = 'sipp-uac-tls');
insert into client_trunks (client_id, name, addresses, tech_prefix, security, tls_cert_name)
  select id, 'sipp-uac-cert', '{172.30.0.21}', '882', 'cert', 'caller.lab.test' from clients where name = 'Lab SIPp'
  and not exists (select 1 from client_trunks where name = 'sipp-uac-cert');

-- Destinations 95…: one vendor reached over three connections at one price,
-- with weights 3, 1 and 0. Calls are shared three to one; the last
-- connection only backs the others.
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  sell bigint := (select id from tariffs where reseller_id = root and name = 'Lab sell');
  plan bigint := (select id from routing_plans where reseller_id = root and name = 'Lab plan');
  t bigint; v bigint; c bigint; spec record;
begin
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (sell, '95', 20000, '2026-01-01') on conflict do nothing;
  insert into tariffs (reseller_id, name, kind) values (root, 'Lab vendor F (shared)', 'buy') on conflict do nothing;
  select id into t from tariffs where reseller_id = root and name = 'Lab vendor F (shared)';
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (t, '95', 9000, '2026-01-01') on conflict do nothing;
  insert into vendors (reseller_id, name, tariff_id) values (root, 'Lab vendor F (shared)', t) on conflict do nothing;
  select id into v from vendors where reseller_id = root and name = 'Lab vendor F (shared)';
  for spec in select * from (values ('w3', 3), ('w1', 1), ('w0', 0)) as s(name, weight) loop
    select id into c from vendor_connections where vendor_id = v and name = spec.name;
    if c is null then
      insert into vendor_connections (vendor_id, name, host) values (v, spec.name, '172.30.0.20') returning id into c;
    end if;
    insert into routing_entries (routing_plan_id, prefix, connection_id, weight) values (plan, '95', c, spec.weight)
      on conflict (routing_plan_id, prefix, connection_id) do update set weight = excluded.weight;
  end loop;
end $$;

-- A client reached with tech prefix 883 that has rules about the caller's
-- number: only callers from 90212… are taken, 90850… is taken and shown to
-- the vendor as 0850…; the account refuses 90212666… for every client.
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  cl bigint;
begin
  insert into clients (reseller_id, name, balance, tariff_id, routing_plan_id)
    select root, 'Lab SIPp CLI', 1000000000, tariff_id, routing_plan_id from clients where reseller_id = root and name = 'Lab SIPp'
    on conflict do nothing;
  select id into cl from clients where reseller_id = root and name = 'Lab SIPp CLI';
  if not exists (select 1 from client_trunks where client_id = cl) then
    insert into client_trunks (client_id, name, addresses, tech_prefix) values (cl, 'sipp-uac-cli', '{172.30.0.21}', '883');
  end if;
  insert into cli_rules (reseller_id, client_id, prefix, action, strip_digits, add_prefix) values
    (root, cl, '', 'block', 0, ''), (root, cl, '90212', 'allow', 0, ''), (root, cl, '90850', 'rewrite', 2, '0'),
    (root, null, '90212666', 'block', 0, '')
    on conflict do nothing;
end $$;

-- Rules on the number the caller asserts (P-Asserted-Identity, T-16). The
-- client of tech prefix 878 takes only callers whose asserted number is
-- 90212… and refuses callers that assert none; From plays no part. The
-- client of tech prefix 876 refuses callers without a number in From.
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  spec record;
  cl bigint;
begin
  for spec in select * from (values ('Lab SIPp PAI', 'sipp-uac-pai', '878'), ('Lab SIPp anonymous', 'sipp-uac-anon', '876')) as s(client, trunk, tech)
  loop
    insert into clients (reseller_id, name, balance, tariff_id, routing_plan_id)
      select root, spec.client, 1000000000, tariff_id, routing_plan_id from clients where reseller_id = root and name = 'Lab SIPp'
      on conflict do nothing;
    select id into cl from clients where reseller_id = root and name = spec.client;
    insert into client_trunks (client_id, name, addresses, tech_prefix)
      select cl, spec.trunk, '{172.30.0.21}', spec.tech where not exists (select 1 from client_trunks where name = spec.trunk);
    if spec.tech = '878' then
      insert into cli_rules (reseller_id, client_id, number_source, prefix, action) values
        (root, cl, 'pai', '', 'block'), (root, cl, 'pai', '90212', 'allow'), (root, cl, 'pai', '', 'anonymous')
        on conflict do nothing;
    else
      insert into cli_rules (reseller_id, client_id, number_source, prefix, action) values (root, cl, 'from', '', 'anonymous')
        on conflict do nothing;
    end if;
  end loop;
end $$;

-- Vendor G: vendor D's address, but expected under a name its certificate
-- does not carry. It is the cheaper route for 94… (vendor D the other) and
-- the only one for 93…. The engine must never send it a call.
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  sell bigint := (select id from tariffs where reseller_id = root and name = 'Lab sell');
  plan bigint := (select id from routing_plans where reseller_id = root and name = 'Lab plan');
  d_tariff bigint := (select id from tariffs where reseller_id = root and name = 'Lab vendor D (TLS)');
  d_conn bigint := (select vc.id from vendor_connections vc join vendors v on v.id = vc.vendor_id
                    where v.reseller_id = root and v.name = 'Lab vendor D (TLS)' and vc.name = 'lab');
  t bigint; v bigint; c bigint;
begin
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values
    (sell, '94', 20000, '2026-01-01'), (sell, '93', 20000, '2026-01-01'), (d_tariff, '94', 9000, '2026-01-01') on conflict do nothing;
  insert into routing_entries (routing_plan_id, prefix, connection_id) values (plan, '94', d_conn) on conflict do nothing;
  insert into tariffs (reseller_id, name, kind) values (root, 'Lab vendor G (wrong name)', 'buy') on conflict do nothing;
  select id into t from tariffs where reseller_id = root and name = 'Lab vendor G (wrong name)';
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (t, '94', 5000, '2026-01-01'), (t, '93', 5000, '2026-01-01') on conflict do nothing;
  insert into vendors (reseller_id, name, tariff_id) values (root, 'Lab vendor G (wrong name)', t) on conflict do nothing;
  select id into v from vendors where reseller_id = root and name = 'Lab vendor G (wrong name)';
  select id into c from vendor_connections where vendor_id = v and name = 'lab';
  if c is null then
    insert into vendor_connections (vendor_id, name, host, port, transport, tls_name)
      values (v, 'lab', '172.30.0.24', 5061, 'tls', 'other.lab.test') returning id into c;
  end if;
  insert into routing_entries (routing_plan_id, prefix, connection_id) values (plan, '94', c), (plan, '93', c) on conflict do nothing;
end $$;

-- Destinations 92…: rates and routes by the hour. The selling tariff has a
-- second, dearer rate with a window, and the plan sends 92… to the refusing
-- vendor A (cheaper) and the answering vendor C, each with a window. The
-- scenario sets the windows around the present moment before it calls.
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  sell bigint := (select id from tariffs where reseller_id = root and name = 'Lab sell');
  plan bigint := (select id from routing_plans where reseller_id = root and name = 'Lab plan');
  spec record; c bigint;
begin
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (sell, '92', 20000, '2026-01-01') on conflict do nothing;
  insert into rates (tariff_id, prefix, price_per_min, initial_sec, increment_sec, effective_from, time_from, time_to)
    select sell, '92', 60000, 60, 60, '2026-01-01', 1, 2
    where not exists (select 1 from rates where tariff_id = sell and prefix = '92' and time_from <> time_to);
  for spec in select * from (values ('Lab vendor A (500)', 5000), ('Lab vendor C (answers)', 12000)) as s(name, price) loop
    insert into rates (tariff_id, prefix, price_per_min, effective_from)
      select t.id, '92', spec.price, '2026-01-01' from tariffs t where t.reseller_id = root and t.name = spec.name on conflict do nothing;
    select vc.id into c from vendor_connections vc join vendors v on v.id = vc.vendor_id where v.reseller_id = root and v.name = spec.name and vc.name = 'lab';
    insert into routing_entries (routing_plan_id, prefix, connection_id) values (plan, '92', c) on conflict do nothing;
  end loop;
end $$;

-- Destinations 91…: vendors in two currencies. Vendor H is paid in EUR
-- (one EUR = 1.20 in the account's currency) and asks 0.010 a minute, which
-- is 0.012 for the account; vendor C asks 0.011. By the figures alone H is
-- the cheaper, by their worth C is: C must be tried first. H refuses (it is
-- the 503 vendor's address), so a wrong order shows as an extra leg.
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  sell bigint := (select id from tariffs where reseller_id = root and name = 'Lab sell');
  plan bigint := (select id from routing_plans where reseller_id = root and name = 'Lab plan');
  t bigint; v bigint; c bigint;
begin
  insert into exchange_rates (reseller_id, currency, rate) values (root, 'EUR', 1200000) on conflict do nothing;
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (sell, '91', 20000, '2026-01-01') on conflict do nothing;
  insert into rates (tariff_id, prefix, price_per_min, effective_from)
    select id, '91', 11000, '2026-01-01' from tariffs where reseller_id = root and name = 'Lab vendor C (answers)' on conflict do nothing;
  insert into routing_entries (routing_plan_id, prefix, connection_id)
    select plan, '91', vc.id from vendor_connections vc join vendors v on v.id = vc.vendor_id
    where v.reseller_id = root and v.name = 'Lab vendor C (answers)' and vc.name = 'lab' on conflict do nothing;
  insert into tariffs (reseller_id, name, kind, currency) values (root, 'Lab vendor H (EUR)', 'buy', 'EUR') on conflict do nothing;
  select id into t from tariffs where reseller_id = root and name = 'Lab vendor H (EUR)';
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (t, '91', 10000, '2026-01-01') on conflict do nothing;
  insert into vendors (reseller_id, name, currency, tariff_id) values (root, 'Lab vendor H (EUR)', 'EUR', t) on conflict do nothing;
  select id into v from vendors where reseller_id = root and name = 'Lab vendor H (EUR)';
  select id into c from vendor_connections where vendor_id = v and name = 'lab';
  if c is null then
    insert into vendor_connections (vendor_id, name, host) values (v, 'lab', '172.30.0.23') returning id into c;
  end if;
  insert into routing_entries (routing_plan_id, prefix, connection_id) values (plan, '91', c) on conflict do nothing;
end $$;

-- A client reached with tech prefix 884 that has no balance, only a bundle
-- of minutes towards 90…. The scenarios set the bundle's size and what the
-- month has used of it before they call.
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  cl bigint;
begin
  insert into clients (reseller_id, name, balance, tariff_id, routing_plan_id)
    select root, 'Lab SIPp bundle', 0, tariff_id, routing_plan_id from clients where reseller_id = root and name = 'Lab SIPp'
    on conflict do nothing;
  select id into cl from clients where reseller_id = root and name = 'Lab SIPp bundle';
  if not exists (select 1 from client_trunks where client_id = cl) then
    insert into client_trunks (client_id, name, addresses, tech_prefix) values (cl, 'sipp-uac-bundle', '{172.30.0.21}', '884');
  end if;
  insert into bundles (client_id, name, prefixes, seconds)
    select cl, 'Lab bundle', '{90}', 600 where not exists (select 1 from bundles where client_id = cl);
end $$;

-- Several addresses and networks. The lab client's trunk also answers to
-- 172.30.0.90 and 172.30.0.83; another client is known by the network
-- 172.30.0.80/30 (.80 to .83). The single address .83 is more specific than
-- the network it lies in, so its calls are the lab client's; .84 is nobody's.
update client_trunks set addresses = '{172.30.0.21,172.30.0.90,172.30.0.83}'
  where name = 'sipp-uac' and addresses <> '{172.30.0.21,172.30.0.83,172.30.0.90}';
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  cl bigint;
begin
  insert into clients (reseller_id, name, balance, tariff_id, routing_plan_id)
    select root, 'Lab SIPp network', 1000000000, tariff_id, routing_plan_id from clients where reseller_id = root and name = 'Lab SIPp'
    on conflict do nothing;
  select id into cl from clients where reseller_id = root and name = 'Lab SIPp network';
  if not exists (select 1 from client_trunks where client_id = cl) then
    insert into client_trunks (client_id, name, addresses) values (cl, 'sipp-uac-net', '{172.30.0.80/30}');
  end if;
end $$;

-- Trunks that register, for the scenarios about address translation (NAT)
-- and calls to a client. Lab only: the passwords are "lab-only-Nat-2026",
-- realm "softswitch".
--   lab-nat      registers from 172.30.0.70 while naming a private address
--   lab-nat-2    a second such trunk, 172.30.0.71: the caller in calls to lab-nat
--   lab-nat-tls  may only be used over TLS: its registration over UDP is refused
--   lab-nat-mix  may register twice, over UDP and over TLS (172.30.0.70 too)
-- Destinations 89… have a rate and a route like any other; the lab's engine
-- delivers 8988… to wherever lab-nat is registered instead, 8987… to
-- lab-nat-tls and 8986… to lab-nat-mix (LAB_DELIVER, cmd/route-engine/lab.go).
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  sell bigint := (select id from tariffs where reseller_id = root and name = 'Lab sell');
  plan bigint := (select id from routing_plans where reseller_id = root and name = 'Lab plan');
  cl bigint; spec record;
begin
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (sell, '89', 20000, '2026-01-01') on conflict do nothing;
  insert into rates (tariff_id, prefix, price_per_min, effective_from)
    select id, '89', 12000, '2026-01-01' from tariffs where reseller_id = root and name = 'Lab vendor C (answers)' on conflict do nothing;
  insert into routing_entries (routing_plan_id, prefix, connection_id)
    select plan, '89', vc.id from vendor_connections vc join vendors v on v.id = vc.vendor_id
    where v.reseller_id = root and v.name = 'Lab vendor C (answers)' and vc.name = 'lab' on conflict do nothing;
  insert into clients (reseller_id, name, balance, tariff_id, routing_plan_id)
    select root, 'Lab SIPp NAT', 1000000000, tariff_id, routing_plan_id from clients where reseller_id = root and name = 'Lab SIPp'
    on conflict do nothing;
  select id into cl from clients where reseller_id = root and name = 'Lab SIPp NAT';
  for spec in select * from (values ('sipp-nat', 'lab-nat', 'any', 1), ('sipp-nat-2', 'lab-nat-2', 'any', 1), ('sipp-nat-tls', 'lab-nat-tls', 'tls', 1), ('sipp-nat-mix', 'lab-nat-mix', 'any', 2)) as s(name, username, security, contacts) loop
    insert into client_trunks (client_id, name, auth_type, username, password_ha1, security, max_contacts)
      select cl, spec.name, 'digest', spec.username, md5(spec.username || ':softswitch:lab-only-Nat-2026'), spec.security, spec.contacts
      where not exists (select 1 from client_trunks where username = spec.username);
  end loop;
end $$;

-- Destinations 87…: the settings of a connection and of a trunk
-- (test/sipp/connlab.sh plays the vendors, on 172.30.0.60–69, .73–79, .85, .86, .88, .89, .100–.106). Vendor S is
-- the cheaper everywhere; where a second route is listed it is the answering
-- vendor C. Each prefix has one connection of vendor S with one setting:
--   8701  rings for ever at .60, ring timeout 5 s                 then C
--   8702  C's address, calls of at most 5 s
--   8703  refuses at .61; every failure moves on                   then C
--   8704  the 500 vendor's address; no failure moves on            then C
--   8705  the 500 vendor's address; only 486 moves on              then C
--   8706  refuses at .61; only 486 moves on                        then C
--   8707  refuses at .61; the standing rule                        then C
--   8708  number sent as +<prefix 77><number less two digits>, checked at .62
--   8709  caller less two digits with 0 in front, asserted identity set to it
--   8710  asserted identity removed
--   8711  first .61 (caller with 11 in front), then .62 (caller with 22 in front)
--   8712  only PCMA is offered
--   8713  PCMA before PCMU
--   8714  only G.729: a caller without it is not sent there        then C
--   8715  signs in at .63 (the scenario sets the password through the API)
--   8716  asked whether it is there, at .64                         then C
--   8717  no setting, checked at .62: for trunks with settings of their own
--   8718  rings for ever at .60; every failure moves on            then C
--         (a caller that gives up must not be moved on all the same)
--   8719  C's address; the lab engine delays its decision (LAB_DECIDE_DELAY)
--   8720  answers at .65 a caller that never acknowledges the answer (ADR-0026)
--   8721  answers at .66 a caller that acknowledges it two seconds late
--   8722  new offers within the call (re-INVITE) at .67; only PCMA is offered
--   8723  new offer towards a caller behind address translation, at .68
--   8724  new offer within a call of at most 5 s, at .69
--   8725  dead call: the media stops, at .73 (ADR-0027)
--   8726  audio all along, at .74
--   8727  held with sendonly, then silent, at .75
--   8728  held with inactive, then silent, at .76
--   8729  fax (T.38) both ways and key presses as INFO, at .77; only PCMA is offered
--   8730  fax datagrams only, for longer than the media timeout, at .78
--   8731  183 with 100rel (PRACK), then a REFER each way, at .79 (ADR-0028)
--   8732  183 with early media and no 100rel, at .85
--   8733  183 with early media, then a minute's silent ringing, at .86; ring timeout 90 s
--   8734  never reached (.87): the media relay is stopped or out of ports (ADR-0030);
--         every failure moves on                                    then C
--   8735  a call of 130 s with audio all along, at .88: the money held is extended twice (T-03)
--   8736  a call with audio, at .89, of the client whose money runs out at the second step
--   8737  PCMU only, translated (transcoding) for a trunk without it, at .100 (T-09)  then C
--   8738  PCMU only, not translated, at .101                         then C
--   8739  PCMA only, translated, at C's address: the load test's transcoding run
--         (TRANSCODE=1 make load; its calls come from trunk 888, PCMU only)
--   8740  a vendor the switch registers with, at .93 (T-14; the scenario
--         turns the registration on through the API and off at its end)
--   8741  INVITE without an offer: the offer comes in the 200, at .102 (T-43);
--         only PCMA is offered
--   8742  new INVITEs without an offer within the call, at .103; only PCMA
--   8743  the caller's ACK does not answer the offer in the 200, at .104
--   8744  the offer in the 200 meets a stopped media relay, at .94
--   8745  progress reported three times, then 503, at .105 (T-46); then .106,
--         progress reported twice, then the answer
-- Trunks of the lab client: 885 calls of at most 5 s; 886 writes numbers
-- with 00 in front; 887 writes them the national way (0…, country 90);
-- 888 speaks PCMU only; 889 one call at a time; 880 speaks PCMA only; 879
-- speaks PCMA only and may be translated.
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  sell bigint := (select id from tariffs where reseller_id = root and name = 'Lab sell');
  plan bigint := (select id from routing_plans where reseller_id = root and name = 'Lab plan');
  cl bigint := (select id from clients where reseller_id = root and name = 'Lab SIPp');
  c_conn bigint := (select vc.id from vendor_connections vc join vendors v on v.id = vc.vendor_id
                    where v.reseller_id = root and v.name = 'Lab vendor C (answers)' and vc.name = 'lab');
  t bigint; v bigint; c bigint; spec record;
begin
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (sell, '87', 20000, '2026-01-01') on conflict do nothing;
  insert into rates (tariff_id, prefix, price_per_min, effective_from)
    select id, '87', 12000, '2026-01-01' from tariffs where reseller_id = root and name = 'Lab vendor C (answers)' on conflict do nothing;
  insert into tariffs (reseller_id, name, kind) values (root, 'Lab vendor S (settings)', 'buy') on conflict do nothing;
  select id into t from tariffs where reseller_id = root and name = 'Lab vendor S (settings)';
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (t, '87', 5000, '2026-01-01') on conflict do nothing;
  insert into vendors (reseller_id, name, tariff_id) values (root, 'Lab vendor S (settings)', t) on conflict do nothing;
  select id into v from vendors where reseller_id = root and name = 'Lab vendor S (settings)';
  for spec in select * from (values
      ('8701', 'ring',     '172.30.0.60', 1, true),
      ('8702', 'short',    '172.30.0.20', 1, false),
      ('8703', 'always',   '172.30.0.61', 1, true),
      ('8704', 'never',    '172.30.0.22', 1, true),
      ('8705', 'custom-a', '172.30.0.22', 1, true),
      ('8706', 'custom-b', '172.30.0.61', 1, true),
      ('8707', 'standard', '172.30.0.61', 1, true),
      ('8708', 'number',   '172.30.0.62', 1, false),
      ('8709', 'cli',      '172.30.0.62', 1, false),
      ('8710', 'no-pai',   '172.30.0.62', 1, false),
      ('8711', 'cli-a',    '172.30.0.61', 1, false),
      ('8711', 'cli-b',    '172.30.0.62', 2, false),
      ('8712', 'pcma',     '172.30.0.62', 1, false),
      ('8713', 'order',    '172.30.0.62', 1, false),
      ('8714', 'g729',     '172.30.0.62', 1, true),
      ('8715', 'sign-in',  '172.30.0.63', 1, true),
      ('8716', 'asked',    '172.30.0.64', 1, true),
      ('8717', 'plain',    '172.30.0.62', 1, false),
      ('8718', 'ring-all', '172.30.0.60', 1, true),
      ('8719', 'slow',     '172.30.0.20', 1, false),
      ('8720', 'unacked',  '172.30.0.65', 1, false),
      ('8721', 'late-ack', '172.30.0.66', 1, false),
      ('8722', 'reinvite', '172.30.0.67', 1, false),
      ('8723', 'reinvite-nat', '172.30.0.68', 1, false),
      ('8724', 'reinvite-short', '172.30.0.69', 1, false),
      ('8725', 'media-dead', '172.30.0.73', 1, false),
      ('8726', 'media-talk', '172.30.0.74', 1, false),
      ('8727', 'media-sendonly', '172.30.0.75', 1, false),
      ('8728', 'media-inactive', '172.30.0.76', 1, false),
      ('8729', 'fax', '172.30.0.77', 1, false),
      ('8730', 'fax-keep', '172.30.0.78', 1, false),
      ('8731', 'prack', '172.30.0.79', 1, false),
      ('8732', 'early', '172.30.0.85', 1, false),
      ('8733', 'early-long', '172.30.0.86', 1, false),
      ('8734', 'no-media', '172.30.0.87', 1, true),
      ('8735', 'reserve-long', '172.30.0.88', 1, false),
      ('8736', 'reserve-short', '172.30.0.89', 1, false),
      ('8737', 'transcode', '172.30.0.100', 1, true),
      ('8738', 'pcmu-only', '172.30.0.101', 1, true),
      ('8739', 'transcode-load', '172.30.0.20', 1, false),
      ('8740', 'registers', '172.30.0.93', 1, false),
      ('8741', 'late', '172.30.0.102', 1, false),
      ('8742', 'late-reinvite', '172.30.0.103', 1, false),
      ('8743', 'late-noanswer', '172.30.0.104', 1, false),
      ('8744', 'late-refused', '172.30.0.94', 1, false),
      ('8745', 'progress', '172.30.0.105', 1, false),
      ('8745', 'progress-answer', '172.30.0.106', 2, false)) as s(prefix, name, host, priority, fallback)
  loop
    select id into c from vendor_connections where vendor_id = v and name = spec.name;
    if c is null then
      insert into vendor_connections (vendor_id, name, host) values (v, spec.name, spec.host) returning id into c;
    end if;
    update vendor_connections set host = spec.host where id = c and host <> spec.host;
    insert into routing_entries (routing_plan_id, prefix, connection_id, priority) values (plan, spec.prefix, c, spec.priority)
      on conflict (routing_plan_id, prefix, connection_id) do update set priority = excluded.priority;
    if spec.fallback then
      insert into routing_entries (routing_plan_id, prefix, connection_id) values (plan, spec.prefix, c_conn) on conflict do nothing;
    end if;
  end loop;
  update vendor_connections set ring_timeout_sec = 5 where vendor_id = v and name = 'ring';
  update vendor_connections set ring_timeout_sec = 90 where vendor_id = v and name = 'early-long';
  update vendor_connections set max_duration_sec = 5 where vendor_id = v and name in ('short', 'reinvite-short');
  update vendor_connections set failover_mode = 'always' where vendor_id = v and name in ('always', 'cli-a', 'ring-all', 'no-media');
  update vendor_connections set failover_mode = 'never' where vendor_id = v and name = 'never';
  update vendor_connections set failover_mode = 'custom', failover_codes = '{486}' where vendor_id = v and name in ('custom-a', 'custom-b');
  update vendor_connections set number_plus = true, strip_digits = 2, tech_prefix = '77' where vendor_id = v and name = 'number';
  update vendor_connections set cli_strip_digits = 2, cli_add_prefix = '0', pai_mode = 'caller' where vendor_id = v and name = 'cli';
  update vendor_connections set pai_mode = 'remove' where vendor_id = v and name = 'no-pai';
  update vendor_connections set cli_add_prefix = '11' where vendor_id = v and name = 'cli-a';
  update vendor_connections set cli_add_prefix = '22' where vendor_id = v and name = 'cli-b';
  update vendor_connections set codecs = '{PCMA}' where vendor_id = v and name in ('pcma', 'reinvite', 'fax', 'late', 'late-reinvite');
  update vendor_connections set codecs = '{PCMA,PCMU}' where vendor_id = v and name = 'order';
  update vendor_connections set codecs = '{G729}' where vendor_id = v and name = 'g729';
  update vendor_connections set codecs = '{PCMU}', transcode = (name = 'transcode') where vendor_id = v and name in ('transcode', 'pcmu-only');
  update vendor_connections set codecs = '{PCMA}', transcode = true where vendor_id = v and name = 'transcode-load';
  update vendor_connections set options_interval_sec = 10 where vendor_id = v and name = 'asked';
  -- Left as a run that stopped half-way may have left it: not registered.
  update vendor_connections set register = false, register_expires_sec = 3600, register_domain = '',
                                auth_username = '', auth_secret = null
    where vendor_id = v and name = 'registers';

  for spec in select * from (values
      ('sipp-uac-short',    '885', 5, '',   '',   '{}'::text[], 0, false),
      ('sipp-uac-00',       '886', 0, '00', '',   '{}',         0, false),
      ('sipp-uac-national', '887', 0, '0',  '90', '{}',         0, false),
      ('sipp-uac-pcmu',     '888', 0, '',   '',   '{PCMU}',     0, false),
      ('sipp-uac-one',      '889', 0, '',   '',   '{}',         1, false),
      ('sipp-uac-pcma',     '880', 0, '',   '',   '{PCMA}',     0, false),
      ('sipp-uac-pcma-x',   '879', 0, '',   '',   '{PCMA}',     0, true)) as s(name, tech, max_duration, strip, add, codecs, max_calls, transcode)
  loop
    insert into client_trunks (client_id, name, addresses, tech_prefix)
      select cl, spec.name, '{172.30.0.21}', spec.tech where not exists (select 1 from client_trunks where name = spec.name);
    update client_trunks set max_duration_sec = spec.max_duration, number_strip = spec.strip, number_add = spec.add,
                             codecs = spec.codecs, max_calls = spec.max_calls, transcode = spec.transcode
      where name = spec.name;
  end loop;
  -- A client that writes numbers three ways at once (T-17): 00…, +… and
  -- the national way, one trunk with three number rules.
  insert into client_trunks (client_id, name, addresses, tech_prefix)
    select cl, 'sipp-uac-multi', '{172.30.0.21}', '877' where not exists (select 1 from client_trunks where name = 'sipp-uac-multi');
  update client_trunks set number_rules = '[{"strip": "0", "add": "90"}, {"strip": "+", "add": ""}, {"strip": "00", "add": ""}]'
    where name = 'sipp-uac-multi';
end $$;

-- A client reached with tech prefix 554 whose money runs out at the second
-- step of its reservation (T-03). Its tariff asks 0.06 a minute towards 87…,
-- one thousandth a second; the scenario sets its balance to 90 seconds'
-- worth (90000) before it calls: 60 seconds are held at the answer, and the
-- second step finds only 30 more.
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  plan bigint := (select id from routing_plans where reseller_id = root and name = 'Lab plan');
  t bigint; cl bigint;
begin
  insert into tariffs (reseller_id, name, kind) values (root, 'Lab reserve', 'sell') on conflict do nothing;
  select id into t from tariffs where reseller_id = root and name = 'Lab reserve';
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (t, '87', 60000, '2026-01-01') on conflict do nothing;
  insert into clients (reseller_id, name, balance, tariff_id, routing_plan_id)
    values (root, 'Lab SIPp reserve', 90000, t, plan) on conflict do nothing;
  select id into cl from clients where reseller_id = root and name = 'Lab SIPp reserve';
  insert into client_trunks (client_id, name, addresses, tech_prefix)
    select cl, 'sipp-uac-554', '{172.30.0.21}', '554' where not exists (select 1 from client_trunks where name = 'sipp-uac-554');
end $$;

-- A carrier that hands calls in (T-04): its connections at 172.30.0.91 and
-- .92 carry no route, they only make the SIP core know the addresses. No
-- trunk matches either; the lab's engine takes calls from .91 on behalf of
-- the client of the trunk "lab-nat" (LAB_INBOUND, cmd/route-engine/lab.go),
-- and calls from .92 stay those of an unknown source.
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  v bigint; spec record;
begin
  insert into vendors (reseller_id, name) values (root, 'Lab carrier (inbound)') on conflict do nothing;
  select id into v from vendors where reseller_id = root and name = 'Lab carrier (inbound)';
  for spec in select * from (values ('taken', '172.30.0.91'), ('unknown', '172.30.0.92')) as s(name, host) loop
    insert into vendor_connections (vendor_id, name, host)
      select v, spec.name, spec.host where not exists (select 1 from vendor_connections where vendor_id = v and name = spec.name);
  end loop;
end $$;

-- IPv6 (T-28), on the lab's IPv6 network fd30::/64 (deploy/compose):
--   fd30::21        the caller: an address of the lab client's trunk "sipp-uac-v6"
--   fd30::8:0/112   a network of the client "Lab SIPp network" (trunk "sipp-uac-net-v6")
--   fd30::8:80/121  a narrower network inside it, the lab client's ("sipp-uac-v6")
--   fd30::20        vendor V6, which answers (sipp-uas-v6)
--   fd30::64        vendor V6's second connection (only PCMA), which makes its
--                   offer in the answer (test/sipp/v6lab.sh plays it)
--   fd30::72        where the trunk "lab-v6" (client "Lab SIPp NAT") registers from;
--                   password "lab-only-Nat-2026", realm "softswitch"
-- Destinations 86…: 8601 to vendor V6, 8602 to vendor C (IPv4), 8604 to
-- vendor V6 at fd30::64; 8603 is delivered to wherever lab-v6 is registered
-- (LAB_DELIVER), else to vendor C.
do $$
declare
  root bigint := (select id from resellers order by id limit 1);
  sell bigint := (select id from tariffs where reseller_id = root and name = 'Lab sell');
  plan bigint := (select id from routing_plans where reseller_id = root and name = 'Lab plan');
  c_tariff bigint := (select id from tariffs where reseller_id = root and name = 'Lab vendor C (answers)');
  c_conn bigint := (select vc.id from vendor_connections vc join vendors v on v.id = vc.vendor_id
                    where v.reseller_id = root and v.name = 'Lab vendor C (answers)' and vc.name = 'lab');
  t bigint; v bigint; c bigint;
begin
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (sell, '86', 20000, '2026-01-01') on conflict do nothing;
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (c_tariff, '86', 12000, '2026-01-01') on conflict do nothing;
  insert into tariffs (reseller_id, name, kind) values (root, 'Lab vendor V6 (answers)', 'buy') on conflict do nothing;
  select id into t from tariffs where reseller_id = root and name = 'Lab vendor V6 (answers)';
  insert into rates (tariff_id, prefix, price_per_min, effective_from) values (t, '86', 12000, '2026-01-01') on conflict do nothing;
  insert into vendors (reseller_id, name, tariff_id) values (root, 'Lab vendor V6 (answers)', t) on conflict do nothing;
  select id into v from vendors where reseller_id = root and name = 'Lab vendor V6 (answers)';
  select id into c from vendor_connections where vendor_id = v and name = 'lab';
  if c is null then
    insert into vendor_connections (vendor_id, name, host) values (v, 'lab', 'fd30::20') returning id into c;
  end if;
  insert into routing_entries (routing_plan_id, prefix, connection_id) values (plan, '8601', c) on conflict do nothing;
  select id into c from vendor_connections where vendor_id = v and name = 'late';
  if c is null then
    insert into vendor_connections (vendor_id, name, host) values (v, 'late', 'fd30::64') returning id into c;
  end if;
  -- Only PCMA, as for vendor S's late offers: the caller checks for it.
  update vendor_connections set codecs = '{PCMA}' where id = c and codecs is distinct from '{PCMA}';
  insert into routing_entries (routing_plan_id, prefix, connection_id) values (plan, '8604', c) on conflict do nothing;
  insert into routing_entries (routing_plan_id, prefix, connection_id) values (plan, '8602', c_conn), (plan, '8603', c_conn) on conflict do nothing;

  insert into client_trunks (client_id, name, addresses)
    select id, 'sipp-uac-v6', '{fd30::21,fd30::8:80/121}' from clients where reseller_id = root and name = 'Lab SIPp'
    and not exists (select 1 from client_trunks where name = 'sipp-uac-v6');
  insert into client_trunks (client_id, name, addresses)
    select id, 'sipp-uac-net-v6', '{fd30::8:0/112}' from clients where reseller_id = root and name = 'Lab SIPp network'
    and not exists (select 1 from client_trunks where name = 'sipp-uac-net-v6');
  insert into client_trunks (client_id, name, auth_type, username, password_ha1)
    select id, 'sipp-v6', 'digest', 'lab-v6', md5('lab-v6:softswitch:lab-only-Nat-2026') from clients where reseller_id = root and name = 'Lab SIPp NAT'
    and not exists (select 1 from client_trunks where username = 'lab-v6');
end $$;
