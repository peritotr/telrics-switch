-- SPDX-License-Identifier: AGPL-3.0-only
-- Rules about the caller's number may look at the number the caller's side
-- asserts (P-Asserted-Identity) instead of the one in From, and may refuse
-- callers without a usable number on their own (T-16). Existing rules look
-- at From, as before.
alter table cli_rules add column number_source text not null default 'from'
  check (number_source in ('from', 'pai'));

alter table cli_rules drop constraint cli_rules_action_check;
alter table cli_rules add constraint cli_rules_action_check
  check (action in ('allow', 'block', 'rewrite', 'anonymous'));

-- A caller without a number has no prefix: such a rule names none. The
-- number shown to vendors is From's, so only a rule on From changes it.
alter table cli_rules add constraint cli_rules_anonymous_check
  check (action <> 'anonymous' or (prefix = '' and strip_digits = 0 and add_prefix = ''));
alter table cli_rules add constraint cli_rules_source_check
  check (number_source = 'from' or action <> 'rewrite');

-- One rule per scope, number and prefix; the refusal of callers without a
-- number stands beside a rule with an empty prefix.
drop index cli_rules_scope_prefix;
create unique index cli_rules_scope_prefix on cli_rules
  (reseller_id, coalesce(client_id, 0), number_source, prefix, (action = 'anonymous'));
