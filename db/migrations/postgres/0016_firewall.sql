-- SPDX-License-Identifier: AGPL-3.0-only
-- The switch's own firewall: addresses and networks the SIP core drops
-- without a word ("deny") or spares its flood protection ("allow"). A rule
-- may end by itself. Only the rules of the account that runs the system
-- (the first one) are applied.
create table firewall_rules (
  id          bigserial primary key,
  reseller_id bigint not null references resellers(id) on delete cascade,
  cidr        text not null check (cidr::inet is not null),
  action      text not null check (action in ('deny', 'allow')),
  note        text not null default '',
  enabled     boolean not null default true,
  expires_at  timestamptz,
  created_at  timestamptz not null default now(),
  unique (reseller_id, cidr)
);
