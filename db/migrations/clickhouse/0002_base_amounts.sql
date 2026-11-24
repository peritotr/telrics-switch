-- SPDX-License-Identifier: AGPL-3.0-only
-- Each leg's amounts in the account's currency, for reports across
-- currencies. Records from before count at their own amounts.
alter table cdrs
  add column if not exists sell_base Int64 default sell_amount,
  add column if not exists buy_base Int64 default buy_amount
