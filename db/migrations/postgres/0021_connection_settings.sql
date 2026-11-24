-- SPDX-License-Identifier: AGPL-3.0-only
-- Detailed settings of a vendor connection and of a client trunk. Every
-- default leaves a row behaving as it did before this migration.

alter table vendor_connections
  -- Signing in to the vendor: the user name, and the password sealed with
  -- the installation's secret key (pkg/totp's format). The password is set
  -- through an operation of its own and never read back.
  add column auth_username text not null default '',
  add column auth_secret   bytea,
  add column auth_set      boolean generated always as (auth_secret is not null) stored,
  -- The dialled number is sent with a leading "+".
  add column number_plus   boolean not null default false,
  -- The caller's number as this vendor is to see it, on top of the rules
  -- about callers: digits taken off the front, then a prefix put there.
  add column cli_strip_digits int not null default 0
    constraint vendor_connections_cli_strip check (cli_strip_digits between 0 and 32),
  add column cli_add_prefix   text not null default ''
    constraint vendor_connections_cli_prefix check (cli_add_prefix ~ '^\+?[0-9]{0,16}$'),
  -- What becomes of P-Asserted-Identity: passed on as it came, removed, or
  -- set to the caller's number as this vendor sees it.
  add column pai_mode text not null default 'pass'
    constraint vendor_connections_pai_mode check (pai_mode in ('pass', 'remove', 'caller')),
  -- How long the vendor may ring before the next route is tried; 0 = the
  -- SIP core's own limit.
  add column ring_timeout_sec int not null default 0
    constraint vendor_connections_ring_timeout check (ring_timeout_sec = 0 or ring_timeout_sec between 5 and 180),
  -- The longest a call over this connection may last; 0 = no limit of its own.
  add column max_duration_sec int not null default 0
    constraint vendor_connections_max_duration check (max_duration_sec = 0 or max_duration_sec between 5 and 7200),
  -- Which answers move a call on to the next route: 'standard' is the rule
  -- there has always been, 'always' every failure, 'never' none, 'custom'
  -- the codes listed.
  add column failover_mode  text not null default 'standard'
    constraint vendor_connections_failover_mode check (failover_mode in ('standard', 'always', 'never', 'custom')),
  add column failover_codes int[] not null default '{}'
    constraint vendor_connections_failover_codes check (300 <= all (failover_codes) and 699 >= all (failover_codes) and cardinality(failover_codes) <= 64),
  -- Seconds between the engine's questions whether the vendor is there
  -- (SIP OPTIONS); 0 = not asked.
  add column options_interval_sec int not null default 0
    constraint vendor_connections_options_interval check (options_interval_sec = 0 or options_interval_sec between 10 and 3600),
  -- Audio codecs offered to the vendor, in order of preference; empty =
  -- whatever the caller offers.
  add column codecs text[] not null default '{}'
    constraint vendor_connections_codecs check (codecs <@ array['PCMA','PCMU','G729','G722','opus','GSM','iLBC','G723','AMR','AMR-WB','speex']);

alter table client_trunks
  -- How the client writes numbers: when a dialled number starts with
  -- number_strip, that is taken off and number_add put in its place; with
  -- an empty number_strip, number_add goes in front of every number.
  add column number_strip text not null default ''
    constraint client_trunks_number_strip check (number_strip ~ '^\+?[0-9]{0,8}$'),
  add column number_add   text not null default ''
    constraint client_trunks_number_add check (number_add ~ '^[0-9]{0,8}$'),
  -- The longest a call from this trunk may last; 0 = no limit of its own.
  add column max_duration_sec int not null default 0
    constraint client_trunks_max_duration check (max_duration_sec = 0 or max_duration_sec between 5 and 7200),
  -- Calls at once and calls a second from this trunk; 0 = no limit of its
  -- own (the client's limits hold in any case).
  add column max_calls int not null default 0 constraint client_trunks_max_calls check (max_calls >= 0),
  add column max_cps   int not null default 0 constraint client_trunks_max_cps check (max_cps >= 0),
  -- Audio codecs used with this client, in order of preference; empty = any.
  add column codecs text[] not null default '{}'
    constraint client_trunks_codecs check (codecs <@ array['PCMA','PCMU','G729','G722','opus','GSM','iLBC','G723','AMR','AMR-WB','speex']);
