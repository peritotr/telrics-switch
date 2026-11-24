-- SPDX-License-Identifier: AGPL-3.0-only
-- People who sign in to the panel with an e-mail address and a password.
-- API keys stay for programs.
create table users (
  id            bigserial primary key,
  reseller_id   bigint not null references resellers(id) on delete cascade,
  email         text not null,
  name          text not null,
  password_hash text not null,            -- bcrypt
  role          text not null default 'readonly' check (role in ('admin','readonly')),
  status        text not null default 'active' check (status in ('active','disabled')),
  failed_logins int not null default 0,   -- since the last success or lock
  locked_until  timestamptz,
  last_login_at timestamptz,
  created_at    timestamptz not null default now()
);
create unique index users_email on users (lower(email));

create table user_sessions (
  id         bigserial primary key,
  user_id    bigint not null references users(id) on delete cascade,
  token_hash bytea not null unique,
  created_at timestamptz not null default now(),
  expires_at timestamptz not null
);
create index user_sessions_expiry on user_sessions (expires_at);
