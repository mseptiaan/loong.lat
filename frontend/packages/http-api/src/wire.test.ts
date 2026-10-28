import { describe, expect, it } from 'vitest'
import { STREAM_METHODS } from '@loong/domain'
import { ARG_NAMES, BACKEND_METHODS, BODY_DEFAULTS, LOCAL_FALLBACK, toBody } from './wire'

describe('badan permintaan', () => {
  it('memetakan argumen posisi ke objek bernama menurut parameter ApiClient', () => {
    expect(toBody('plans.saveDraft', ['pln-1', { draftStep: 3 }])).toEqual({ id: 'pln-1', patch: { draftStep: 3 } })
    expect(toBody('auth.loginCustomer', ['dina@contoh.id', 'rahasia'])).toEqual({ email: 'dina@contoh.id', password: 'rahasia' })
    expect(toBody('driver.complete', ['p', 'r', 's', { kind: 'otp', value: '1234' }])).toEqual({ planId: 'p', routeId: 'r', stopId: 's', proof: { kind: 'otp', value: '1234' } })
  })

  it('menghilangkan argumen yang tidak diberikan, dan metode tanpa parameter berbadan kosong', () => {
    expect(toBody('driver.reportIssue', ['p', 'r', 's', 'tidak_ada_orang'])).toEqual({ planId: 'p', routeId: 'r', stopId: 's', issue: 'tidak_ada_orang' })
    expect(toBody('plans.list', [undefined])).toEqual({})
    expect(toBody('plans.createDraft', [])).toEqual({})
  })

  it('setiap parameter dari metode yang dilayani backend punya nama', () => {
    // metode berparameter yang dikirim ke backend; tanpa entri, argumennya akan hilang diam-diam
    const withArgs = [
      'auth.loginCustomer', 'auth.register', 'auth.loginAdmin', 'auth.loginDriver', 'wallet.createTopUp', 'wallet.getTopUp', 'wallet.cancelTopUp',
      'plans.get', 'plans.saveDraft', 'plans.computeOptions', 'plans.chooseOption', 'plans.confirm', 'plans.remove',
      'fleet.vehicles.create', 'fleet.vehicles.update', 'fleet.vehicles.remove', 'fleet.drivers.create', 'fleet.drivers.update',
      'dispatch.suggest', 'dispatch.assign', 'dispatch.unassign', 'dispatch.autoAssign', 'settings.update',
      'driver.setOnline', 'driver.startRoute', 'driver.arrive', 'driver.complete', 'driver.reportIssue', 'driver.ping',
      'plans.cancel', 'plans.reschedule', 'account.update', 'account.addresses.add', 'account.addresses.update', 'account.addresses.remove',
      'notifications.markRead', 'users.list', 'users.get', 'users.setBlocked', 'finance.topUps', 'finance.transactions',
      'ai.readImage', 'ai.transcribe', 'ai.parseSpeech', 'ai.parseIntent', 'ai.copilot', 'ai.explainOptions',
    ]
    const served = new Set([...BACKEND_METHODS.console, ...BACKEND_METHODS.backoffice, ...BACKEND_METHODS.driver])
    for (const m of withArgs) {
      expect(served.has(m), `${m} dilayani backend`).toBe(true)
      expect(ARG_NAMES[m], `${m} punya nama parameter`).toBeDefined()
    }
  })

  it('metode baru dipetakan ke nama parameter ApiClient', () => {
    expect(toBody('plans.reschedule', ['p1', { mode: 'sekarang' }])).toEqual({ id: 'p1', schedule: { mode: 'sekarang' } })
    expect(toBody('account.addresses.update', ['a1', { label: 'Toko' }])).toEqual({ id: 'a1', patch: { label: 'Toko' } })
    expect(toBody('notifications.markRead', [['n1', 'n2']])).toEqual({ ids: ['n1', 'n2'] })
    expect(toBody('notifications.markRead', [])).toEqual({}) // tanpa ids = semua
    expect(toBody('users.setBlocked', ['u1', false])).toEqual({ id: 'u1', blocked: false }) // false tetap dikirim
    expect(toBody('finance.transactions', ['u1'])).toEqual({ userId: 'u1' })
    expect(toBody('ai.copilot', [{ kind: 'plan', planId: 'p1' }, 'kapan sampai?'])).toEqual({ scope: { kind: 'plan', planId: 'p1' }, question: 'kapan sampai?' })
    expect(toBody('ai.parseSpeech', ['dua kardus'])).toEqual({ transcript: 'dua kardus' })
    expect(toBody('ai.parseIntent', ['paling hemat'])).toEqual({ text: 'paling hemat' })
  })

  it('plans.computeOptions: opsi disebar ke badan ({id, effort}), bawaan fast, kolom kosong dihilangkan', () => {
    expect(toBody('plans.computeOptions', ['p1', { effort: 'thorough' }])).toEqual({ id: 'p1', effort: 'thorough' })
    expect(toBody('plans.computeOptions', ['p1', { effort: 'reviewed' }])).toEqual({ id: 'p1', effort: 'reviewed' })
    expect(toBody('plans.computeOptions', ['p1'])).toEqual({ id: 'p1', effort: 'fast' })
    expect(toBody('plans.computeOptions', ['p1', {}])).toEqual({ id: 'p1', effort: 'fast' })
    expect(toBody('plans.computeOptions', ['p1', { effort: undefined }])).toEqual({ id: 'p1', effort: 'fast' })
    // tidak ada kunci "opts" di kawat
    expect(Object.keys(toBody('plans.computeOptions', ['p1', { effort: 'thorough' }]))).toEqual(['id', 'effort'])
    // sebaran hanya untuk objek; selain itu diabaikan
    expect(toBody('plans.computeOptions', ['p1', 'thorough'])).toEqual({ id: 'p1', effort: 'fast' })
    expect(BODY_DEFAULTS['plans.computeOptions']).toEqual({ effort: 'fast' })
    // metode lain tidak mendapat nilai bawaan
    expect(toBody('plans.get', ['p1'])).toEqual({ id: 'p1' })
  })

  it('ai.explainOptions: badan {planId}, aliran, dan hanya console yang melayani', () => {
    expect(toBody('ai.explainOptions', ['pln-7'])).toEqual({ planId: 'pln-7' })
    expect(STREAM_METHODS.has('ai.explainOptions')).toBe(true)
    expect(STREAM_METHODS.has('plans.computeOptions')).toBe(true)
    expect(BACKEND_METHODS.console.has('ai.explainOptions')).toBe(true)
    expect(BACKEND_METHODS.backoffice.has('ai.explainOptions')).toBe(false)
    expect(BACKEND_METHODS.driver.has('ai.explainOptions')).toBe(false)
    expect(LOCAL_FALLBACK.has('ai.explainOptions')).toBe(false) // laporan tidak pernah dikarang klien
  })

  it('metode yang baru dilayani backend dikirim ke API yang tepat', () => {
    const forConsole = ['plans.cancel', 'plans.reschedule', 'account.profile', 'account.update', 'account.addresses.add', 'account.addresses.update', 'account.addresses.remove', 'notifications.list', 'notifications.markRead', 'settings.get', 'ai.readImage', 'ai.parseSpeech', 'ai.parseIntent', 'ai.transcribe', 'ai.copilot', 'ai.explainOptions']
    const forBackoffice = ['plans.cancel', 'users.list', 'users.get', 'users.setBlocked', 'finance.overview', 'finance.topUps', 'finance.transactions', 'notifications.list', 'notifications.markRead', 'ai.copilot', 'ai.insights', 'settings.get', 'settings.update']
    const forDriver = ['notifications.list', 'notifications.markRead', 'settings.get']
    for (const m of forConsole) expect(BACKEND_METHODS.console.has(m), `console ${m}`).toBe(true)
    for (const m of forBackoffice) expect(BACKEND_METHODS.backoffice.has(m), `backoffice ${m}`).toBe(true)
    for (const m of forDriver) expect(BACKEND_METHODS.driver.has(m), `driver ${m}`).toBe(true)
    // yang tetap di klien tidak pernah dikirim: pencarian tempat dan jam nyata
    for (const app of ['console', 'backoffice', 'driver'] as const) {
      expect(BACKEND_METHODS[app].has('places.search')).toBe(false)
      expect(BACKEND_METHODS[app].has('demo.state')).toBe(false)
    }
    // pengguna, keuangan, dan insight hanya milik backoffice-api; foto/suara/maksud hanya console-api
    expect(BACKEND_METHODS.console.has('users.list')).toBe(false)
    expect(BACKEND_METHODS.console.has('ai.insights')).toBe(false)
    expect(BACKEND_METHODS.backoffice.has('ai.readImage')).toBe(false)
  })

  it('cadangan lokal hanya untuk foto, suara, dan maksud; Copilot, wawasan, dan transkripsi tidak punya', () => {
    expect([...LOCAL_FALLBACK].sort()).toEqual(['ai.parseIntent', 'ai.parseSpeech', 'ai.readImage'])
  })

  it('tiap aplikasi hanya mengirim ke API-nya sendiri', () => {
    expect(BACKEND_METHODS.console.has('wallet.get')).toBe(true)
    expect(BACKEND_METHODS.console.has('fleet.vehicles.list')).toBe(false) // armada dan pengemudinya bukan untuk pelanggan
    expect(BACKEND_METHODS.console.has('fleet.drivers.list')).toBe(false)
    expect(BACKEND_METHODS.console.has('fleet.vehicles.create')).toBe(false)
    expect(BACKEND_METHODS.backoffice.has('dispatch.assign')).toBe(true)
    expect(BACKEND_METHODS.backoffice.has('plans.confirm')).toBe(false)
    expect(BACKEND_METHODS.driver.has('driver.ping')).toBe(true)
    expect(BACKEND_METHODS.driver.has('plans.get')).toBe(false)
  })
})
