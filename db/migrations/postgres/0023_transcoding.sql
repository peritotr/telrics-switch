-- SPDX-License-Identifier: AGPL-3.0-only
-- Codec translation (transcoding, T-09). A connection or a trunk that allows
-- it lets a call through where the trunk's and the connection's codec lists
-- have nothing in common: the media relay translates between them. Off by
-- default, so every row keeps the behaviour it had: such a call is not
-- routed there.
alter table vendor_connections add column transcode boolean not null default false;
alter table client_trunks add column transcode boolean not null default false;
