-- SPDX-License-Identifier: AGPL-3.0-only
-- How far back call records may have left PostgreSQL for ClickHouse (the
-- rater's CDR_KEEP_DAYS, T-24): every record that started at or after the
-- horizon is still here. The rater raises it each time it deletes; the API
-- reads older records from ClickHouse. No row: nothing was ever archived,
-- and every list keeps reading PostgreSQL alone.
create table cdr_archive (
  one     boolean primary key default true check (one),
  horizon timestamptz not null
);
