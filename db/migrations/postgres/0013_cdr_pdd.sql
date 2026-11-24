-- SPDX-License-Identifier: AGPL-3.0-only
-- Post-dial delay of each attempt: the time the far end took to report
-- progress, in milliseconds. Null or 0: it never did, or the record is from
-- before.
alter table cdrs add column pdd_ms int;
