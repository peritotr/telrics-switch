-- SPDX-License-Identifier: AGPL-3.0-only
-- The client's trunk an attempt was delivered to instead of a vendor
-- (routing.ToTrunk). Null on a leg to a vendor and in every record from
-- before this migration. No foreign key: a record keeps what the engine
-- reported, so that a trunk removed while its call was being rated cannot
-- turn the record away (call records are never lost).
alter table cdrs add column dst_trunk_id bigint;
