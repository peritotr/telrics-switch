-- SPDX-License-Identifier: AGPL-3.0-only
-- What a client trunk's calls must arrive over: anything ('any'), TLS
-- ('tls'), or TLS with a client certificate carrying a given name ('cert').
alter table client_trunks
  add column security      text not null default 'any' check (security in ('any','tls','cert')),
  add column tls_cert_name text not null default '',
  add constraint client_trunks_cert_name check (security <> 'cert' or tls_cert_name <> '');
