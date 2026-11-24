-- SPDX-License-Identifier: AGPL-3.0-only
-- Weights share calls among the routes of one priority; a negative one
-- means nothing.
update routing_entries set weight = 0 where weight < 0;
alter table routing_entries add constraint routing_entries_weight_check check (weight between 0 and 1000000);
