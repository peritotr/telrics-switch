-- SPDX-License-Identifier: AGPL-3.0-only
-- The name a TLS vendor's certificate must be made out to, when it is not
-- the connection's host (a vendor reached by address, say).
alter table vendor_connections add column tls_name text not null default '';
