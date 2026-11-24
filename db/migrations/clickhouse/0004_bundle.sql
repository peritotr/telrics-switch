-- SPDX-License-Identifier: AGPL-3.0-only
-- Seconds of the call paid for by a bundle of minutes, and which one.
alter table cdrs add column if not exists bundle_id UInt64 default 0, add column if not exists bundle_sec UInt32 default 0
