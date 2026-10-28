import { describe, expect, it } from 'vitest'
import type { ApiClient } from '@loong/domain'
import { createHttpApi, type HttpApiOptions } from './client'
import { chunked, fakeBackend, json } from './fake'
import { normalizeResult } from './normalize'

type App = HttpApiOptions['app']

function setup(app: App, answers: Record<string, unknown> = {}) {
  const backend = fakeBackend()
  backend.handle('/healthz', () => json({ ok: true }))
  const m = new Map<string, string>([['loong:http:' + app, JSON.stringify({ token: 'tok', session: { role: 'customer', userId: 'u', name: 'U' } })]])
  const storage = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), removeItem: (k: string) => void m.delete(k) }
  for (const [method, value] of Object.entries(answers)) backend.handle(`/v1/rpc/${method}`, () => json(value))
  const api = createHttpApi({ app, baseUrl: 'http://api.test', fetch: backend.fetch, storage, retryMs: 50 })
  return { api, backend }
}

/** [metode, panggilan, badan yang diharapkan]: tiap metode baru sampai ke backend dengan nama parameter ApiClient. */
type Case = [string, (api: ApiClient) => unknown, unknown]

const consoleCases: Case[] = [
  ['plans.cancel', (a) => a.plans.cancel('p1', 'berubah pikiran'), { id: 'p1', reason: 'berubah pikiran' }],
  ['plans.cancel', (a) => a.plans.cancel('p1'), { id: 'p1' }],
  ['plans.reschedule', (a) => a.plans.reschedule('p1', { mode: 'sekarang' }), { id: 'p1', schedule: { mode: 'sekarang' } }],
  ['account.profile', (a) => a.account.profile(), {}],
  ['account.update', (a) => a.account.update({ name: 'Dina' }), { patch: { name: 'Dina' } }],
  ['account.addresses.update', (a) => a.account.addresses.update('adr-1', { label: 'Toko' }), { id: 'adr-1', patch: { label: 'Toko' } }],
  ['account.addresses.remove', (a) => a.account.addresses.remove('adr-1'), { id: 'adr-1' }],
  ['notifications.list', (a) => a.notifications.list(), {}],
  ['notifications.markRead', (a) => a.notifications.markRead(['n1']), { ids: ['n1'] }],
  ['notifications.markRead', (a) => a.notifications.markRead(), {}],
  ['settings.get', (a) => a.settings.get(), {}],
]

const backofficeCases: Case[] = [
  ['plans.cancel', (a) => a.plans.cancel('p1', 'duplikat'), { id: 'p1', reason: 'duplikat' }],
  ['users.list', (a) => a.users.list('dina'), { q: 'dina' }],
  ['users.list', (a) => a.users.list(), {}],
  ['users.get', (a) => a.users.get('u1'), { id: 'u1' }],
  ['users.setBlocked', (a) => a.users.setBlocked('u1', false), { id: 'u1', blocked: false }],
  ['finance.overview', (a) => a.finance.overview(), {}],
  ['finance.topUps', (a) => a.finance.topUps({ status: ['menunggu'], userId: 'u1' }), { filter: { status: ['menunggu'], userId: 'u1' } }],
  ['finance.transactions', (a) => a.finance.transactions('u1'), { userId: 'u1' }],
  ['notifications.list', (a) => a.notifications.list(), {}],
  ['notifications.markRead', (a) => a.notifications.markRead(), {}],
  ['settings.get', (a) => a.settings.get(), {}],
]

const driverCases: Case[] = [
  ['notifications.list', (a) => a.notifications.list(), {}],
  ['notifications.markRead', (a) => a.notifications.markRead(['n9']), { ids: ['n9'] }],
  ['settings.get', (a) => a.settings.get(), {}],
]

describe.each<[App, Case[]]>([
  ['console', consoleCases],
  ['backoffice', backofficeCases],
  ['driver', driverCases],
])('metode yang baru dilayani backend: %s', (app, cases) => {
  it.each(cases)('%s dikirim ke API-nya dengan badan bernama', async (method, call, body) => {
    // bentuk jawaban mengikuti metode; rapikan nilai kosong supaya normalisasi tidak mengubah apa yang diuji
    const { api, backend } = setup(app, { [method]: method.endsWith('.list') || method === 'finance.transactions' ? [] : {} })
    await call(api as unknown as ApiClient)
    const calls = backend.rpc(method)
    expect(calls).toHaveLength(1)
    expect(calls[0]?.body).toEqual(body)
    expect(calls[0]?.headers.authorization).toBe('Bearer tok')
    api.destroy()
  })
})

describe('aliran Copilot dan wawasan', () => {
  const ndjson = () => chunked(['{"type":"token","text":"Halo"}\n', '{"type":"done","actions":[],"requestId":"r1"}\n'], 'application/x-ndjson')

  it('ai.copilot (console dan backoffice) mengalir dengan {scope, question}', async () => {
    for (const app of ['console', 'backoffice'] as const) {
      const { api, backend } = setup(app)
      backend.handle('/v1/rpc/ai.copilot', ndjson)
      const got: unknown[] = []
      for await (const e of api.ai.copilot({ kind: 'plan', planId: 'p1' }, 'kapan sampai?')) got.push(e)
      expect(got).toEqual([{ type: 'token', text: 'Halo' }, { type: 'done', actions: [], requestId: 'r1' }])
      expect(backend.rpc('ai.copilot')[0]?.body).toEqual({ scope: { kind: 'plan', planId: 'p1' }, question: 'kapan sampai?' })
      api.destroy()
    }
  })

  it('ai.insights (backoffice) mengalir dengan badan kosong', async () => {
    const { api, backend } = setup('backoffice')
    backend.handle('/v1/rpc/ai.insights', ndjson)
    const got: unknown[] = []
    for await (const e of api.ai.insights()) got.push(e)
    expect(got).toHaveLength(2)
    expect(backend.rpc('ai.insights')[0]?.body).toEqual({})
    api.destroy()
  })

  it('Copilot dan wawasan tidak punya cadangan lokal: galat layanan diteruskan', async () => {
    const { api, backend } = setup('console')
    backend.handle('/v1/rpc/ai.copilot', () => json({ code: 'layanan', msg: { key: 'srv.api.aiError' } }, 503))
    await expect(
      (async () => {
        for await (const e of api.ai.copilot({ kind: 'ops' }, 'x')) void e
      })(),
    ).rejects.toMatchObject({ code: 'layanan' })
    api.destroy()
  })
})

describe('jawaban backend dirapikan di perbatasan', () => {
  it('account.profile: savedAddresses null menjadi daftar kosong, sehingga layar Alamat tidak pecah', async () => {
    const { api } = setup('console', { 'account.profile': { id: 'u', name: 'Dina', email: 'd@x.id', phone: '0812', createdAt: '2026-01-01T00:00:00Z', blocked: false, savedAddresses: null } })
    const profile = await api.account.profile()
    expect(profile.savedAddresses).toEqual([])
    expect(profile.name).toBe('Dina')
    api.destroy()
  })

  it('users.list dan notifikasi: null (slice nil di Go) menjadi []', async () => {
    const bo = setup('backoffice', { 'users.list': null, 'notifications.list': null, 'finance.topUps': null, 'finance.transactions': null })
    expect(await bo.api.users.list()).toEqual([])
    expect(await bo.api.notifications.list()).toEqual([])
    expect(await bo.api.finance.topUps()).toEqual([])
    expect(await bo.api.finance.transactions('u1')).toEqual([])
    bo.api.destroy()
  })

  it('normalizeResult: angka nol yang dihilangkan menjadi 0, waktu nol menjadi tidak ada', () => {
    const [row] = normalizeResult('users.list', [{ id: 'u', name: 'A', email: 'a@x.id', createdAt: '2026-01-01T00:00:00Z', blocked: false, lastPlanAt: '0001-01-01T00:00:00Z' }]) as Record<string, unknown>[]
    expect(row).toMatchObject({ phone: '', blocked: false, plansCount: 0, completedCount: 0, totalSpendRp: 0, balanceRp: 0 })
    expect('lastPlanAt' in row!).toBe(false)

    const kept = normalizeResult('users.setBlocked', { id: 'u', blocked: true, balanceRp: 5000, lastPlanAt: '2026-10-01T03:00:00Z' }) as Record<string, unknown>
    expect(kept).toMatchObject({ balanceRp: 5000, lastPlanAt: '2026-10-01T03:00:00Z', blocked: true })

    expect(normalizeResult('finance.overview', { totalBalanceRp: 1000 })).toEqual({
      totalBalanceRp: 1000, usersWithBalance: 0, pendingCount: 0, pendingAmountRp: 0, paidTodayRp: 0, paidTodayCount: 0, paid30dRp: 0, paid30dCount: 0,
    })
    expect(normalizeResult('finance.overview', null)).toMatchObject({ totalBalanceRp: 0, paid30dCount: 0 })
  })

  it('users.get dan finance.topUps: plans null, alamat null, nama kosong, paidAt nol', () => {
    const detail = normalizeResult('users.get', { user: { id: 'u', name: 'A', savedAddresses: null }, summary: { id: 'u' }, plans: null }) as { user: { savedAddresses: unknown[] }; summary: { balanceRp: number }; plans: unknown[] }
    expect(detail.plans).toEqual([])
    expect(detail.user.savedAddresses).toEqual([])
    expect(detail.summary.balanceRp).toBe(0)

    const [topUp] = normalizeResult('finance.topUps', [{ id: 't', userId: 'usr-9', status: 'menunggu', paidAt: '0001-01-01T00:00:00Z' }]) as Record<string, unknown>[]
    expect(topUp).toMatchObject({ userName: 'usr-9', userEmail: '', reference: '' }) // tanpa nama: id pengguna, bukan kosong
    expect('paidAt' in topUp!).toBe(false)
  })

  it('settings.get: saklar yang dihilangkan berarti mati (bukan menyala), bagian yang hilang memakai bawaan', () => {
    expect(normalizeResult('settings.get', { ai: { vision: true, copilot: true }, dispatch: {} })).toEqual({
      ai: { vision: true, voice: false, intent: false, copilot: true },
      dispatch: { auto: false },
    })
    expect(normalizeResult('settings.get', {})).toEqual({
      ai: { vision: true, voice: true, intent: true, copilot: true },
      dispatch: { auto: true },
    })
    const full = { ai: { vision: false, voice: true, intent: true, copilot: false }, dispatch: { auto: true } }
    expect(normalizeResult('settings.update', full)).toEqual(full)
  })

  it('metode lain dan nilai yang sudah benar lewat tanpa diubah', () => {
    const plan = { id: 'p', routes: null }
    expect(normalizeResult('plans.get', plan)).toBe(plan)
    const list = [{ id: 'n1', read: true }]
    expect(normalizeResult('notifications.list', list)).toBe(list)
  })
})
