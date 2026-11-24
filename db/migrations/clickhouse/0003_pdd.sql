-- SPDX-License-Identifier: AGPL-3.0-only
-- Post-dial delay of each attempt in milliseconds; 0 = none measured.
alter table cdrs add column if not exists pdd_ms UInt32 default 0
