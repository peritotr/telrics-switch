-- SPDX-License-Identifier: AGPL-3.0-only
-- Call records are copied to the analytics store (ClickHouse) through a
-- queue filled in the same transaction that stores the record, so a record
-- is exported if and only if it was stored.

-- Holds one row once an exporter has been set up; without it nothing is
-- queued and the queue cannot grow unread.
create table cdr_export_enabled (
  one boolean primary key default true check (one)
);

create table cdr_export_queue (
  cdr_id bigint primary key references cdrs(id) on delete cascade
);
