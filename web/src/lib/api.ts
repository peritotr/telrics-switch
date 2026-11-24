// SPDX-License-Identifier: AGPL-3.0-only
// Client for the management API. The key lives in memory only: closing or
// reloading the tab ends the session.
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { t } from './i18n'

export type Row = Record<string, any>
export type Params = Record<string, string | number | boolean | null | undefined>

export class ApiError extends Error {
  /** The fixed word some refusals carry, e.g. "two_step_required". */
  constructor(public status: number, message: string, public code?: string) {
    super(message)
  }
}

let token = ''
let onUnauthorized = () => {}
let onTwoStepRequired = () => {}
export const setToken = (t: string) => { token = t }
export const getToken = () => token
export const setUnauthorizedHandler = (f: () => void) => { onUnauthorized = f }
/** Called when the account starts requiring two-step sign-in of a session that has not set it up. */
export const setTwoStepRequiredHandler = (f: () => void) => { onTwoStepRequired = f }
const twoStepRequired = 'two_step_required'

const query = (params?: Params) => {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params ?? {})) if (v != null && v !== '') q.set(k, String(v))
  const s = q.toString()
  return s ? `?${s}` : ''
}

export async function request<T = any>(
  method: string, path: string, opts: { params?: Params; body?: unknown; text?: string; key?: string } = {},
): Promise<{ data: T; total: number | null }> {
  let resp: Response
  try {
    resp = await fetch(path + query(opts.params), {
      method,
      headers: {
        Authorization: `Bearer ${opts.key ?? token}`,
        ...(opts.body !== undefined ? { 'Content-Type': 'application/json' } : {}),
        ...(opts.text !== undefined ? { 'Content-Type': 'text/csv' } : {}),
      },
      body: opts.text ?? (opts.body !== undefined ? JSON.stringify(opts.body) : undefined),
    })
  } catch {
    throw new ApiError(0, t('api.unreachable'))
  }
  if (resp.status === 401 && !opts.key) onUnauthorized()
  const text = await resp.text()
  let data: any = null
  try { data = text ? JSON.parse(text) : null } catch { /* a proxy error page */ }
  if (resp.status === 403 && data?.code === twoStepRequired && !opts.key) onTwoStepRequired()
  if (!resp.ok) throw new ApiError(resp.status, data?.error ?? `HTTP ${resp.status}`, data?.code)
  const total = resp.headers.get('X-Total-Count')
  return { data, total: total == null ? null : Number(total) }
}

/** Fetches a file with the session key and hands it to the browser as a download. */
export async function download(path: string, params: Params, filename: string) {
  let resp: Response
  try {
    resp = await fetch(path + query(params), { headers: { Authorization: `Bearer ${token}` } })
  } catch {
    throw new ApiError(0, t('api.unreachable'))
  }
  if (resp.status === 401) onUnauthorized()
  if (!resp.ok) {
    let message = `HTTP ${resp.status}`
    let code: string | undefined
    try { const body = await resp.json(); message = body.error ?? message; code = body.code } catch { /* not JSON */ }
    if (resp.status === 403 && code === twoStepRequired) onTwoStepRequired()
    throw new ApiError(resp.status, message, code)
  }
  const url = URL.createObjectURL(await resp.blob())
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

export const api = {
  get: <T = any>(path: string, params?: Params) => request<T>('GET', path, { params }).then((r) => r.data),
  post: <T = any>(path: string, body?: unknown, params?: Params) => request<T>('POST', path, { body: body ?? {}, params }).then((r) => r.data),
  patch: <T = any>(path: string, body: unknown) => request<T>('PATCH', path, { body }).then((r) => r.data),
  put: <T = any>(path: string, body: unknown) => request<T>('PUT', path, { body }).then((r) => r.data),
  del: (path: string) => request('DELETE', path).then(() => undefined),
}

export function useGet<T = any>(path: string | null, params?: Params) {
  return useQuery({ queryKey: [path, params ?? {}], queryFn: () => api.get<T>(path!, params), enabled: path != null })
}

/** A server-paged list. One row more than a page is requested to learn whether a next page exists. */
export function usePaged(path: string, params: Params, pageSize = 25) {
  const [page, setPage] = useState(0)
  const key = JSON.stringify(params)
  const [seen, setSeen] = useState(key)
  if (seen !== key) { setSeen(key); setPage(0) }
  const q = useQuery({
    queryKey: [path, params, page, pageSize],
    queryFn: () => request<Row[]>('GET', path, { params: { ...params, limit: pageSize + 1, offset: page * pageSize } }),
    placeholderData: keepPreviousData,
  })
  const all = q.data?.data ?? []
  return {
    rows: all.slice(0, pageSize), total: q.data?.total ?? null, page, setPage, pageSize,
    hasNext: all.length > pageSize, loading: q.isPending, error: q.error as ApiError | null, refetch: q.refetch,
  }
}
export type Paged = ReturnType<typeof usePaged>

/** Small reference lists (tariffs, plans, parties) by id, for names and select boxes. */
export function useLookup(path: string, params?: Params) {
  const q = useGet<Row[]>(path, { ...params, limit: 1000 })
  const rows = q.data ?? []
  const byId = new Map<number, Row>(rows.map((r) => [r.id, r]))
  return { rows, byId, name: (id: number | null | undefined) => (id == null ? '—' : byId.get(id)?.name ?? `#${id}`) }
}

/** Refetches every query whose path starts with one of the prefixes. */
export function useInvalidate() {
  const qc = useQueryClient()
  return (...prefixes: string[]) =>
    qc.invalidateQueries({ predicate: (q) => prefixes.some((p) => String(q.queryKey[0] ?? '').startsWith(p)) })
}

export interface Session {
  edition: string
  /** Whoever is signed in, a person or an API key; the role decides the rights. */
  key: { id: number; name: string; role: 'admin' | 'readonly' }
  /** Present when a person signed in with an e-mail address and a password. */
  user?: {
    id: number; name: string; email: string; role: 'admin' | 'readonly'; two_step?: boolean
    /** The account requires two-step sign-in and this user has not set it up: only the set-up is open. */
    two_step_setup_required?: boolean
  }
  account: Row
}
