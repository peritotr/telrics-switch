-- SPDX-License-Identifier: AGPL-3.0-only
-- Registering with a vendor (T-14, ADR-0035): a vendor that takes calls only
-- from a switch registered with it. The SIP core sends the REGISTER, with
-- the connection's sign-in name and password (auth_username, auth_secret);
-- the address of record is the sign-in name at register_domain, or at the
-- connection's host when that is empty. Every existing row keeps its
-- behaviour: nothing is registered.
alter table vendor_connections
  add column register boolean not null default false,
  -- The time asked for; the vendor's answer may grant a shorter one.
  add column register_expires_sec int not null default 3600
    constraint vendor_connections_register_expires check (register_expires_sec between 60 and 86400),
  add column register_domain text not null default ''
    constraint vendor_connections_register_domain check (register_domain ~ '^[A-Za-z0-9.-]{0,253}$');
