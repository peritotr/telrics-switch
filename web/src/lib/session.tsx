// SPDX-License-Identifier: AGPL-3.0-only
import { createContext, useContext, type ReactNode } from 'react'
import type { Row, Session } from './api'
import type { Messages } from './i18n'

export type { Session }

export interface NavItem {
  /** Menu group id: monitoring, parties, pricing, numbers, tools or system (the Turkish group names still work). */
  group: string
  /** A message key of the core or the extension's catalogue, or plain text. */
  label: string
  path?: string
  /** Shown greyed out with a "yakında" tag; has no page yet. */
  soon?: boolean
}

export interface DetailTab {
  key: string
  /** A message key or plain text, as NavItem.label. */
  label: string
  render: (row: Row) => ReactNode
}

/**
 * What another package may add to the panel. The Community panel ships with
 * none; an edition built on top of it passes its own to createPanel.
 */
export interface PanelExtension {
  /**
   * The extension's interface texts. Its labels (menu items, tabs, status and
   * audit names) may be keys of this catalogue; its screens read them with
   * `tx`. An English text missing here shows the Turkish one.
   */
  messages?: Messages
  nav?: NavItem[]
  routes?: { path: string; element: ReactNode }[]
  clientTabs?: DetailTab[]
  vendorTabs?: DetailTab[]
  /** Narrows the menu for the signed-in session, e.g. by account type. */
  filterNav?: (items: NavItem[], session: Session) => NavItem[]
  /** Extra content in the top bar, before the clock. */
  topBar?: (session: Session) => ReactNode
  /** Extra form section on the client "General" tab; returns fields to merge into the PATCH body. */
  clientForm?: (props: { values: Row; set: (key: string, value: any) => void; disabled: boolean }) => ReactNode
}

interface Ctx {
  session: Session
  canEdit: boolean
  logout: () => void
  ext: PanelExtension[]
}

export const SessionContext = createContext<Ctx>(null!)
export const useSession = () => useContext(SessionContext)
