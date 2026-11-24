-- SPDX-License-Identifier: AGPL-3.0-only
-- The signalling of calls, one row per SIP message, as the SIP core copied
-- it to the trace collector. Kept for a week unless TRACE_KEEP_DAYS says
-- otherwise (the collector changes the TTL to match when it starts); a day
-- is dropped as a whole once all of it is past that.
create table if not exists sip_messages (
  at        DateTime64(6, 'UTC'),
  seq       UInt64,
  call_id   String,
  direction LowCardinality(String),
  transport LowCardinality(String),
  src_ip    String,
  src_port  UInt16,
  dst_ip    String,
  dst_port  UInt16,
  raw       String codec(ZSTD(3))
) engine = MergeTree
partition by toDate(at)
order by (call_id, at, seq)
ttl toDateTime(at) + interval 7 day
settings ttl_only_drop_parts = 1
