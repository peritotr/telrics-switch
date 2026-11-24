// SPDX-License-Identifier: AGPL-3.0-only
import { createContext, useContext, type ReactNode } from 'react'
import type { Row, Session } from './api'

export type { Session }

export interface NavItem {
  group: string
  label: string
  path?: string
  /** Shown greyed out with a "yakında" tag; has no page yet. */
  soon?: boolean
}

export interface DetailTab {
  key: string
  label: string
  render: (row: Row) => ReactNode
}

/**
 * What another package may add to the panel. The Community panel ships with
 * none; an edition built on top of it passes its own to createPanel.
 */
export interface PanelExtension {
  nav?: NavItem[]
  routes?: { path: string; element: ReactNode }[]
  clientTabs?: DetailTab[]
  vendorTabs?: DetailTab[]
  /** Narrows the menu for the signed-in session, e.g. by account type. */
  filterNav?: (items: NavItem[], session: Session) => NavItem[]
  /** Extra content in the top bar, before the clock. */
  topBar?: (session: Session) => ReactNode
  /** Extra form section on the client "Genel" tab; returns fields to merge into the PATCH body. */
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
