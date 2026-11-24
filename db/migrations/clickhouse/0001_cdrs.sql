-- SPDX-License-Identifier: AGPL-3.0-only
-- Call records for search and reports. A record may arrive more than once;
-- rows with the same sorting key are merged, the highest id winning, so
-- queries that must not see a duplicate read with FINAL.
create table if not exists cdrs (
  id              UInt64,
  call_id         String,
  leg             UInt16,
  direction       LowCardinality(String),
  client_id       UInt64,
  trunk_id        UInt64,
  vendor_id       UInt64,
  connection_id   UInt64,
  src             String,
  dst             String,
  src_ip          String,
  sell_prefix     String,
  buy_prefix      String,
  start_at        DateTime64(6, 'UTC'),
  answer_at       Nullable(DateTime64(6, 'UTC')),
  end_at          DateTime64(6, 'UTC'),
  duration_sec    UInt32,
  sip_code        UInt16,
  sell_billed_sec UInt32,
  buy_billed_sec  UInt32,
  sell_amount     Int64,
  buy_amount      Int64
) engine = ReplacingMergeTree(id)
partition by toYYYYMM(start_at)
order by (client_id, start_at, call_id, leg)
