-- SPDX-License-Identifier: AGPL-3.0-only
-- Fields and indexes the management panel reads.
alter table cdrs add column src_ip text not null default '';

-- The call list is read newest first, with or without a party filter.
create index cdrs_time on cdrs (start_at desc, id desc);
create index cdrs_call_id on cdrs (call_id);
create index rates_tariff_prefix on rates (tariff_id, prefix text_pattern_ops, effective_from desc);
