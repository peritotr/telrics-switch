-- SPDX-License-Identifier: AGPL-3.0-only
-- Who changed what, and when: one row per change made through the
-- management API, written in the transaction that makes the change.
-- reseller_id is the account of whoever acted; it is not a foreign key, so
-- that the record of an account's last days outlives the account.
-- changes holds, per field, {"old": …, "new": …}; secrets are written as
-- "***" by the program and never reach this table.
create table audit_log (
  id          bigserial primary key,
  at          timestamptz not null default now(),
  reseller_id bigint not null,
  actor_type  text not null check (actor_type in ('user', 'key', 'system')),
  actor_id    bigint,                    -- users.id or api_keys.id; kept after that row is gone
  actor_name  text not null default '',  -- the e-mail address or the key's name at the time
  ip          text not null default '',
  resource    text not null,             -- as in the API's paths: clients, rates, users, …
  row_id      bigint,
  action      text not null,             -- create, update, delete, or a named operation
  label       text not null default '',  -- what people call the row: its name, prefix, address
  changes     jsonb,
  detail      jsonb
);
create index audit_log_account on audit_log (reseller_id, at desc, id desc);
create index audit_log_at on audit_log (at);
