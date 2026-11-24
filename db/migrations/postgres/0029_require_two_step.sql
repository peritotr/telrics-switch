-- SPDX-License-Identifier: AGPL-3.0-only
-- An account may require two-step sign-in of its panel users (T-23,
-- ADR-0045). Until a user has it on, a session of theirs may only set it
-- up (or end); API keys are not affected. Off for every existing account,
-- so that nothing changes until an administrator turns it on.
alter table resellers add column require_two_step boolean not null default false;
