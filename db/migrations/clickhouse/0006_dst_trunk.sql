-- SPDX-License-Identifier: AGPL-3.0-only
-- The client's trunk an attempt was delivered to instead of a vendor; 0 =
-- none (a vendor leg, or a record from before the column).
alter table cdrs add column if not exists dst_trunk_id UInt64 default 0
