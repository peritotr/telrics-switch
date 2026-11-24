-- SPDX-License-Identifier: AGPL-3.0-only
-- Rules about the caller's number: refuse it, let it pass, or change what
-- the vendor is shown. A rule belongs to one client or, with no client, to
-- every client of the account; the longest matching prefix decides.
create table cli_rules (
  id           bigserial primary key,
  reseller_id  bigint not null references resellers(id) on delete cascade,
  client_id    bigint references clients(id) on delete cascade,
  prefix       text not null default '' check (prefix ~ '^[0-9]*$'),
  action       text not null check (action in ('allow', 'block', 'rewrite')),
  strip_digits int not null default 0 check (strip_digits between 0 and 32),
  add_prefix   text not null default '' check (add_prefix ~ '^[0-9]*$'),
  note         text not null default '',
  enabled      boolean not null default true,
  created_at   timestamptz not null default now(),
  constraint cli_rules_rewrite_check check (action = 'rewrite' or (strip_digits = 0 and add_prefix = ''))
);
create unique index cli_rules_scope_prefix on cli_rules (reseller_id, coalesce(client_id, 0), prefix);
create index cli_rules_client on cli_rules (client_id);
