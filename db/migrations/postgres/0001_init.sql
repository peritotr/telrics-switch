-- SPDX-License-Identifier: AGPL-3.0-only
-- Money is int8 in micro-units (1/1,000,000 of the currency unit). No floats.
-- Balance conventions:
--   clients: funds available (usage decreases it; may go negative up to -credit_limit)
--   vendors: amount we owe the vendor (usage increases it; our payments decrease it)

-- The account that owns everything else. Community has exactly one row, the
-- operator, created at bootstrap.
create table resellers (
  id         bigserial primary key,
  name       text not null,
  currency   char(3) not null default 'USD',
  status     text not null default 'active' check (status in ('active','suspended')),
  created_at timestamptz not null default now()
);

create table api_keys (
  id          bigserial primary key,
  reseller_id bigint not null references resellers(id) on delete cascade,
  name        text not null,
  key_hash    bytea not null unique,
  role        text not null default 'admin' check (role in ('admin','readonly')),
  created_at  timestamptz not null default now()
);

create table tariffs (
  id          bigserial primary key,
  reseller_id bigint not null references resellers(id),
  name        text not null,
  kind        text not null check (kind in ('sell','buy')),
  currency    char(3) not null default 'USD',
  created_at  timestamptz not null default now(),
  unique (reseller_id, name)
);

create table rates (
  id             bigserial primary key,
  tariff_id      bigint not null references tariffs(id) on delete cascade,
  prefix         text not null check (prefix ~ '^[0-9]*$'),
  price_per_min  bigint not null check (price_per_min >= 0),
  connect_fee    bigint not null default 0 check (connect_fee >= 0),
  initial_sec    int not null default 1 check (initial_sec >= 0),
  increment_sec  int not null default 1 check (increment_sec >= 1),
  grace_sec      int not null default 0 check (grace_sec >= 0),
  blocked        boolean not null default false,
  effective_from timestamptz not null default now(),
  effective_to   timestamptz,
  unique (tariff_id, prefix, effective_from)
);

create table routing_plans (
  id          bigserial primary key,
  reseller_id bigint not null references resellers(id),
  name        text not null,
  policy      text not null default 'lcr' check (policy in ('lcr','priority')),
  allow_loss  boolean not null default false,   -- route even when buy > sell
  created_at  timestamptz not null default now(),
  unique (reseller_id, name)
);

create table vendors (
  id           bigserial primary key,
  reseller_id  bigint not null references resellers(id),
  name         text not null,
  currency     char(3) not null default 'USD',
  balance      bigint not null default 0,
  tariff_id    bigint references tariffs(id),
  email        text, tax_id text, address text,
  status       text not null default 'active' check (status in ('active','suspended')),
  created_at   timestamptz not null default now(),
  unique (reseller_id, name)
);

create table vendor_connections (
  id           bigserial primary key,
  vendor_id    bigint not null references vendors(id) on delete cascade,
  name         text not null,
  host         text not null,
  port         int not null default 5060,
  transport    text not null default 'udp' check (transport in ('udp','tcp','tls')),
  tech_prefix  text not null default '',
  strip_digits int not null default 0,
  max_calls    int not null default 0,   -- 0 = unlimited
  max_cps      int not null default 0,
  enabled      boolean not null default true,
  created_at   timestamptz not null default now()
);

create table routing_entries (
  id              bigserial primary key,
  routing_plan_id bigint not null references routing_plans(id) on delete cascade,
  prefix          text not null check (prefix ~ '^[0-9]*$'),
  connection_id   bigint not null references vendor_connections(id) on delete cascade,
  priority        int not null default 10,
  weight          int not null default 1,
  enabled         boolean not null default true,
  unique (routing_plan_id, prefix, connection_id)
);

create table clients (
  id              bigserial primary key,
  reseller_id     bigint not null references resellers(id),
  name            text not null,
  billing_mode    text not null default 'prepaid' check (billing_mode in ('prepaid','postpaid')),
  currency        char(3) not null default 'USD',
  balance         bigint not null default 0,
  credit_limit    bigint not null default 0,
  tariff_id       bigint references tariffs(id),
  routing_plan_id bigint references routing_plans(id),
  max_calls       int not null default 0,
  max_cps         int not null default 0,
  email           text, tax_id text, address text,
  status          text not null default 'active' check (status in ('active','suspended','closed')),
  created_at      timestamptz not null default now(),
  unique (reseller_id, name)
);

create table client_trunks (
  id           bigserial primary key,
  client_id    bigint not null references clients(id) on delete cascade,
  name         text not null,
  auth_type    text not null default 'ip' check (auth_type in ('ip','digest')),
  ip           inet,
  username     text unique,
  password_ha1 text,
  tech_prefix  text not null default '',
  enabled      boolean not null default true,
  created_at   timestamptz not null default now(),
  check ((auth_type = 'ip' and ip is not null) or (auth_type = 'digest' and username is not null))
);
create unique index client_trunks_ip_prefix on client_trunks (ip, tech_prefix) where auth_type = 'ip';

create table cdrs (
  id               bigserial primary key,
  call_id          text not null,
  leg              int not null default 1,
  direction        text not null default 'outbound' check (direction in ('outbound')),
  client_id        bigint references clients(id),
  trunk_id         bigint references client_trunks(id),
  vendor_id        bigint references vendors(id),
  connection_id    bigint references vendor_connections(id),
  src              text not null default '',
  dst              text not null,
  sell_prefix      text, buy_prefix text,
  start_at         timestamptz not null,
  answer_at        timestamptz,
  end_at           timestamptz not null,
  duration_sec     int not null default 0,
  sip_code         int not null default 200,
  sell_billed_sec  int not null default 0,
  buy_billed_sec   int not null default 0,
  sell_amount      bigint not null default 0,
  buy_amount       bigint not null default 0,
  unique (call_id, leg)
);
create index cdrs_client_time on cdrs (client_id, start_at);
create index cdrs_vendor_time on cdrs (vendor_id, start_at);

create table ledger (
  id            bigserial primary key,
  party_type    text not null check (party_type in ('client','vendor')),
  party_id      bigint not null,
  amount        bigint not null,            -- signed change applied to the balance
  balance_after bigint not null,
  kind          text not null check (kind in ('payment','usage','adjustment','refund')),
  ref           text not null default '',
  note          text not null default '',
  created_at    timestamptz not null default now()
);
create index ledger_party on ledger (party_type, party_id, id);
