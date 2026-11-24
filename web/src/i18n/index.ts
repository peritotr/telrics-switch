// SPDX-License-Identifier: AGPL-3.0-only
// The panel's message catalogues, one file per area (see lib/i18n.tsx). A
// new area file is added here; `npm run check-i18n` rejects a key defined in
// two files.
import * as alerts from './alerts'
import * as audit from './audit'
import * as cdrs from './cdrs'
import * as clients from './clients'
import * as clirules from './clirules'
import * as common from './common'
import * as firewall from './firewall'
import * as holidays from './holidays'
import * as live from './live'
import * as login from './login'
import * as overview from './overview'
import * as plans from './plans'
import * as rates from './rates'
import * as schedules from './schedules'
import * as settings from './settings'
import * as simulator from './simulator'
import * as system from './system'
import * as tariffs from './tariffs'
import * as trace from './trace'
import * as users from './users'
import * as vendors from './vendors'

const areas = [common, alerts, audit, cdrs, clients, clirules, firewall, holidays, live, login, overview, plans, rates, schedules, settings, simulator, system, tariffs, trace, users, vendors] as const

type Merge<A extends readonly { tr: object }[]> = A extends readonly [infer H extends { tr: object }, ...infer R extends { tr: object }[]] ? H['tr'] & Merge<R> : unknown

export const tr = Object.assign({}, ...areas.map((a) => a.tr)) as Merge<typeof areas>
export const en = Object.assign({}, ...areas.map((a) => a.en)) as Record<keyof typeof tr, string>
