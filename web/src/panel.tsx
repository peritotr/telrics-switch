// SPDX-License-Identifier: AGPL-3.0-only
// The panel shell: session, navigation and routes. createPanel is the entry
// point other editions build on.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { BrowserRouter, NavLink, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { getToken, request, setToken, setTwoStepRequiredHandler, setUnauthorizedHandler, type Session } from './lib/api'
import { dt } from './lib/format'
import { LangContext, addMessages, chooseLang, startLang, t, tx, type Lang } from './lib/i18n'
import { SessionContext, type NavItem, type PanelExtension } from './lib/session'
import { AuditLog } from './screens/AuditLog'
import { BalanceAlerts } from './screens/BalanceAlerts'
import { ReportSchedules } from './screens/ReportSchedules'
import { SystemResources } from './screens/SystemResources'
import { Firewall } from './screens/Firewall'
import { CallTrace } from './screens/SipFlow'
import { Cdrs } from './screens/Cdrs'
import { ClientDetail, Clients } from './screens/Clients'
import { LiveCalls } from './screens/LiveCalls'
import { LiveMonitor } from './screens/LiveMonitor'
import { Login, TwoStepRequired } from './screens/Login'
import { Overview } from './screens/Overview'
import { CliRules } from './screens/CliRules'
import { Holidays } from './screens/Holidays'
import { PlanDetail, Plans } from './screens/Plans'
import { Settings, applyPrefs } from './screens/Settings'
import { Simulator } from './screens/Simulator'
import { TariffDetail, Tariffs } from './screens/Tariffs'
import { Users } from './screens/Users'
import { VendorDetail, Vendors } from './screens/Vendors'
import { Badge, LangSwitch, UIProvider } from './ui/kit'

/** Menu groups in order. A NavItem names its group by one of these ids. */
const groups = ['monitoring', 'parties', 'pricing', 'numbers', 'tools', 'system']

// Group names extensions used before the groups had ids.
const legacyGroups: Record<string, string> = {
  'İzleme': 'monitoring', 'Taraflar': 'parties', 'Fiyat ve rota': 'pricing', 'Numara ve fatura': 'numbers', 'Araçlar': 'tools', 'Sistem': 'system',
}
const groupOf = (i: NavItem) => legacyGroups[i.group] ?? i.group
const groupName = (g: string) => tx(`nav.group.${g}`)

const coreNav: NavItem[] = [
  { group: 'monitoring', label: 'nav.overview', path: '/' },
  { group: 'monitoring', label: 'nav.cdrs', path: '/cdrs' },
  { group: 'monitoring', label: 'nav.live', path: '/live' },
  { group: 'monitoring', label: 'nav.calls', path: '/calls' },
  { group: 'parties', label: 'nav.clients', path: '/clients' },
  { group: 'parties', label: 'nav.vendors', path: '/vendors' },
  { group: 'pricing', label: 'nav.tariffs', path: '/tariffs' },
  { group: 'pricing', label: 'nav.plans', path: '/plans' },
  { group: 'pricing', label: 'nav.cliRules', path: '/cli-rules' },
  { group: 'pricing', label: 'nav.holidays', path: '/holidays' },
  { group: 'pricing', label: 'nav.simulator', path: '/simulator' },
  { group: 'tools', label: 'nav.system', path: '/tools/system' },
  { group: 'tools', label: 'nav.firewall', path: '/tools/firewall' },
  { group: 'tools', label: 'nav.trace', path: '/tools/trace' },
  { group: 'system', label: 'nav.settings', path: '/settings' },
  { group: 'system', label: 'nav.alerts', path: '/alerts' },
  { group: 'system', label: 'nav.schedules', path: '/report-schedules' },
  { group: 'system', label: 'nav.users', path: '/users' },
  { group: 'system', label: 'nav.audit', path: '/audit-log' },
]

const coreRoutes: { path: string; element: ReactNode }[] = [
  { path: '/', element: <Overview /> },
  { path: '/cdrs', element: <Cdrs /> },
  { path: '/live', element: <LiveMonitor /> },
  { path: '/calls', element: <LiveCalls /> },
  { path: '/clients', element: <Clients /> },
  { path: '/clients/:id', element: <ClientDetail /> },
  { path: '/vendors', element: <Vendors /> },
  { path: '/vendors/:id', element: <VendorDetail /> },
  { path: '/tariffs', element: <Tariffs /> },
  { path: '/tariffs/:id', element: <TariffDetail /> },
  { path: '/plans', element: <Plans /> },
  { path: '/plans/:id', element: <PlanDetail /> },
  { path: '/cli-rules', element: <CliRules /> },
  { path: '/holidays', element: <Holidays /> },
  { path: '/simulator', element: <Simulator /> },
  { path: '/settings', element: <Settings /> },
  { path: '/alerts', element: <BalanceAlerts /> },
  { path: '/report-schedules', element: <ReportSchedules /> },
  { path: '/tools/system', element: <SystemResources /> },
  { path: '/tools/firewall', element: <Firewall /> },
  { path: '/tools/trace', element: <CallTrace /> },
  { path: '/users', element: <Users /> },
  { path: '/audit-log', element: <AuditLog /> },
]

const editions: Record<string, string> = { community: 'Community', enterprise: 'Enterprise' }

function Clock() {
  const [now, setNow] = useState(() => new Date())
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), 20_000)
    return () => clearInterval(t)
  }, [])
  return <div className="mono tx3" style={{ fontSize: 12, whiteSpace: 'nowrap' }}>{dt(now)} UTC</div>
}

function GlobalSearch() {
  const [text, setText] = useState('')
  const navigate = useNavigate()
  const go = () => {
    const q = text.trim()
    if (!q) return
    if (/^\+?\d{4,}$/.test(q)) navigate(`/cdrs?dst=${q.replace('+', '')}`)
    else if (q.includes('@') || q.length > 24) navigate(`/cdrs?call_id=${encodeURIComponent(q)}`)
    else navigate(`/clients?q=${encodeURIComponent(q)}`)
    setText('')
  }
  return <input className="input" style={{ width: 280, background: 'var(--bg)' }} placeholder={t('shell.search')}
    value={text} onChange={(e) => setText(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && go()} />
}

function Shell({ session, nav, routes, logout, topBar, lang }: { session: Session; nav: NavItem[]; routes: { path: string; element: ReactNode }[]; logout: () => void; topBar: ReactNode; lang: Lang }) {
  const { pathname } = useLocation()
  const active = (item: NavItem) => item.path != null && (item.path === '/' ? pathname === '/' : pathname === item.path || pathname.startsWith(item.path + '/'))
  const current = nav.find(active)
  const readonly = session.key.role !== 'admin'
  return (
    <div className="shell">
      <aside className="side">
        <div className="brand">
          <div className="brand-mark" />
          <div className="brand-name">Telrics Switch</div>
          <div className="edition">{editions[session.edition] ?? session.edition}</div>
        </div>
        <nav className="nav">
          {groups.map((g) => {
            const items = nav.filter((i) => groupOf(i) === g)
            if (!items.length) return null
            return (
              <div key={g}>
                <div className="nav-group">{groupName(g)}</div>
                {[...items.filter((i) => !i.soon), ...items.filter((i) => i.soon)].map((i) => i.soon
                  ? <div key={i.label} className="nav-item soon"><span className="grow ell">{tx(i.label)}</span><span className="nav-tag">{t('nav.soon')}</span></div>
                  : <NavLink key={i.label} to={i.path!} className={'nav-item' + (active(i) ? ' active' : '')}><span className="grow ell">{tx(i.label)}</span></NavLink>)}
              </div>
            )
          })}
        </nav>
        <div className="side-foot">
          <div className={session.user ? 'ell' : 'mono ell'} style={{ fontSize: 12 }} title={session.user?.email}>{session.key.name}</div>
          <div className="row tx3" style={{ fontSize: 11.5 }}>
            <span className="grow ell">{session.user ? '' : t('shell.apiKey')}{readonly ? t('shell.readonly') : t('shell.admin')}</span>
            <a onClick={logout}>{t('shell.logout')}</a>
          </div>
        </div>
      </aside>
      <div className="main">
        <header className="top">
          <div className="tx3 ell">{current ? `${groupName(groupOf(current))} › ${tx(current.label)}` : ''}</div>
          <div className="grow" />
          <GlobalSearch />
          {readonly && <Badge tone="warn">{t('shell.readonly')}</Badge>}
          {topBar}
          <LangSwitch />
          <Clock />
        </header>
        {/* Remounted on a change of language, so that every page, an extension's too, renders anew in it. */}
        <main className="content" id="mainscroll" key={lang}>
          <Routes>
            {routes.map((r) => <Route key={r.path} path={r.path} element={r.element} />)}
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </div>
    </div>
  )
}

const devKey = 'tsw.dev-session'

function Root({ ext }: { ext: PanelExtension[] }) {
  const [lang, setLangState] = useState(startLang)
  const setLang = (l: Lang) => { chooseLang(l); setLangState(l) }
  const [session, setSession] = useState<Session | null>(null)
  const client = useMemo(() => new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false, staleTime: 5_000 } },
  }), [])
  const login = (key: string, s: Session) => {
    setToken(key)
    setSession(s)
    if (import.meta.env.DEV) sessionStorage.setItem(devKey, key)
  }
  const logout = () => {
    // Ends the session on the server too; an API key has none and ignores it.
    // Sent with the key named, so a refusal cannot call logout again.
    const key = getToken()
    if (key) request('POST', '/v1/logout', { key }).catch(() => undefined)
    setToken('')
    client.clear()
    setSession(null)
    if (import.meta.env.DEV) sessionStorage.removeItem(devKey)
  }
  // Asks the server who is signed in again, e.g. once two-step sign-in is set up.
  const recheck = async () => {
    const key = getToken()
    if (!key) return
    const r = await request<Session>('GET', '/v1/session', { key })
    if (getToken() !== key) return
    // What was read before the hold may be refused or stale afterwards.
    if (r.data.user?.two_step_setup_required) client.clear()
    setSession(r.data)
  }
  useEffect(() => {
    applyPrefs()
    setUnauthorizedHandler(logout)
    // The account started requiring two-step sign-in while the panel was open.
    setTwoStepRequiredHandler(() => { recheck().catch(() => undefined) })
    // Development only: survive the reloads a code change causes. Production
    // builds never store the key.
    // A password reset link always opens the reset page, never a saved session.
    const saved = import.meta.env.DEV && window.location.pathname !== '/reset' ? sessionStorage.getItem(devKey) : null
    if (saved) request<Session>('GET', '/v1/session', { key: saved }).then((r) => login(saved, r.data), () => sessionStorage.removeItem(devKey))
  }, []) // eslint-disable-line react-hooks/exhaustive-deps
  const langs = (children: ReactNode) => <LangContext.Provider value={{ lang, setLang }}>{children}</LangContext.Provider>
  if (!session) return langs(<Login onLogin={login} />)
  if (session.user?.two_step_setup_required) return langs(<UIProvider><TwoStepRequired session={session} recheck={recheck} logout={logout} /></UIProvider>)
  const nav = ext.reduce((items, e) => e.filterNav?.(items, session) ?? items, [...coreNav, ...ext.flatMap((e) => e.nav ?? [])])
  const routes = [...coreRoutes, ...ext.flatMap((e) => e.routes ?? [])]
  return langs(
    <QueryClientProvider client={client}>
      <SessionContext.Provider value={{ session, canEdit: session.key.role === 'admin', logout, ext }}>
        <UIProvider>
          <BrowserRouter>
            <Shell session={session} nav={nav} routes={routes} logout={logout} lang={lang} topBar={ext.map((e, i) => <span key={i}>{e.topBar?.(session)}</span>)} />
          </BrowserRouter>
        </UIProvider>
      </SessionContext.Provider>
    </QueryClientProvider>
  )
}

/** Builds the panel, optionally with extensions that add screens, menu items and tabs. */
export function createPanel(...extensions: PanelExtension[]) {
  for (const e of extensions) if (e.messages) addMessages(e.messages)
  return function Panel() {
    return <Root ext={extensions} />
  }
}
