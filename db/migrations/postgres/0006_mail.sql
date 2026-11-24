-- SPDX-License-Identifier: AGPL-3.0-only
-- Outgoing e-mail waits here, written in the transaction that causes it,
-- and is sent by the API process with retries.
create table mail_outbox (
  id          bigserial primary key,
  to_addr     text not null,
  subject     text not null,
  body        text not null,
  attempts    int not null default 0,
  last_error  text,
  next_try_at timestamptz not null default now(),
  sent_at     timestamptz,
  created_at  timestamptz not null default now()
);
create index mail_outbox_due on mail_outbox (next_try_at) where sent_at is null;

-- A panel user who forgot the password asks for a link that sets a new one.
create table password_resets (
  token_hash bytea primary key,
  user_id    bigint not null references users(id) on delete cascade,
  expires_at timestamptz not null,
  used_at    timestamptz
);
