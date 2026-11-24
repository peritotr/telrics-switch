-- SPDX-License-Identifier: AGPL-3.0-only
-- A trunk that signs in with a user name may register: tell the switch
-- where it is right now, so that it can be reached there. How many
-- addresses it may have registered at once is set per trunk; the
-- registrations themselves are short-lived and live in the engine's state
-- store, not here.
alter table client_trunks add column max_contacts int not null default 1
  constraint client_trunks_max_contacts check (max_contacts between 1 and 10);
