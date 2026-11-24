-- SPDX-License-Identifier: AGPL-3.0-only
-- Two-step sign-in for panel users: a TOTP secret per user, single-use
-- recovery codes, and the short-lived challenge between the password and
-- the code at sign-in.
-- totp_secret and totp_pending hold the secret sealed by the program: one
-- version byte, then the secret itself (0) or nonce and AES-GCM text (1).
alter table users
  add column totp_on        boolean not null default false,
  add column totp_secret    bytea,
  add column totp_pending   bytea,   -- set-up started, not yet confirmed with a code
  add column totp_last_step bigint not null default 0; -- last accepted time step: a code works once

create table user_recovery_codes (
  user_id   bigint not null references users(id) on delete cascade,
  code_hash bytea not null,
  used_at   timestamptz,
  primary key (user_id, code_hash)
);

create table user_login_challenges (
  token_hash bytea primary key,
  user_id    bigint not null references users(id) on delete cascade,
  expires_at timestamptz not null
);
