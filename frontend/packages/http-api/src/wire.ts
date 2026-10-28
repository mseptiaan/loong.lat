import type { AppId } from '@loong/domain'

/**
 * Bentuk permintaan ke backend asli. Badan sebuah panggilan adalah SATU objek bernama menurut parameter TypeScript di
 * `ApiClient` (domain/api.ts), misalnya `plans.saveDraft(id, patch)` -> `{"id":"...","patch":{...}}`. Tabel ini adalah
 * satu-satunya tempat pemetaan posisi -> nama itu hidup; backend mengikuti nama yang sama (backend/services/README.md).
 * Nama yang diawali `...` menyebarkan isi objek argumennya ke badan: `plans.computeOptions(id, { effort })` -> `{"id":"...","effort":"..."}`.
 */
export const ARG_NAMES: Readonly<Record<string, readonly string[]>> = {
  'auth.loginCustomer': ['email', 'password'],
  'auth.register': ['input'],
  'auth.loginAdmin': ['email', 'password'],
  'auth.loginDriver': ['phone', 'pin'],

  'wallet.createTopUp': ['input'],
  'wallet.getTopUp': ['id'],
  'wallet.cancelTopUp': ['id'],
  'wallet.simulatePay': ['id'],

  'plans.list': ['filter'],
  'plans.get': ['id'],
  'plans.saveDraft': ['id', 'patch'],
  'plans.computeOptions': ['id', '...opts'],
  'plans.chooseOption': ['id', 'optionId'],
  'plans.confirm': ['id', 'paymentMethod'],
  'plans.cancel': ['id', 'reason'],
  'plans.reschedule': ['id', 'schedule'],
  'plans.remove': ['id'],

  'fleet.vehicles.create': ['input'],
  'fleet.vehicles.update': ['id', 'patch'],
  'fleet.vehicles.remove': ['id'],
  'fleet.drivers.create': ['input'],
  'fleet.drivers.update': ['id', 'patch'],

  'dispatch.suggest': ['planId', 'routeId'],
  'dispatch.assign': ['planId', 'routeId', 'driverId'],
  'dispatch.unassign': ['planId', 'routeId'],
  'dispatch.autoAssign': ['planId'],

  'driver.setOnline': ['online'],
  'driver.startRoute': ['planId', 'routeId'],
  'driver.arrive': ['planId', 'routeId', 'stopId'],
  'driver.complete': ['planId', 'routeId', 'stopId', 'proof'],
  'driver.reportIssue': ['planId', 'routeId', 'stopId', 'issue', 'note'],
  'driver.ping': ['position', 'speedKmh'],

  'account.update': ['patch'],
  'account.addresses.add': ['input'],
  'account.addresses.update': ['id', 'patch'],
  'account.addresses.remove': ['id'],

  'notifications.markRead': ['ids'],

  'users.list': ['q'],
  'users.get': ['id'],
  'users.setBlocked': ['id', 'blocked'],

  'finance.topUps': ['filter'],
  'finance.transactions': ['userId'],

  'settings.update': ['patch'],

  // Biner (foto, suara) dikirim sebagai base64 di dalam `input`; lihat media.ts.
  'ai.readImage': ['input'],
  'ai.transcribe': ['input'],
  'ai.parseSpeech': ['transcript'],
  'ai.parseIntent': ['text'],
  'ai.copilot': ['scope', 'question'],
  'ai.explainOptions': ['planId'],
}

/** Nilai bawaan badan bila argumen tidak mengisinya: tingkat pencarian bawaan selalu dikirim eksplisit (`fast`). */
export const BODY_DEFAULTS: Readonly<Record<string, Readonly<Record<string, unknown>>>> = {
  'plans.computeOptions': { effort: 'fast' },
}

/** Kunci pesan galat "belum ada di backend" untuk metode yang belum dilayani siapa pun. */
export const UNAVAILABLE_KEY = 'srv.api.notAvailableYet'

type ApiApp = Extract<AppId, 'console' | 'backoffice' | 'driver'>

const set = (...names: string[]): ReadonlySet<string> => new Set(names)

/**
 * Metode yang dilayani backend asli, per aplikasi. Tiap aplikasi hanya bicara ke API-nya sendiri
 * (console -> console-api, backoffice -> backoffice-api, driver -> driver-api); yang tidak ada di sini
 * dijawab klien sendiri (local.ts) atau ditolak dengan jelas, tidak pernah dikirim ke layanan yang salah.
 *
 * Yang tetap di klien: `places.*` (gazetteer) dan `demo.state` (jam nyata). Panel Demo tidak ada di mode http.
 */
export const BACKEND_METHODS: Readonly<Record<ApiApp, ReadonlySet<string>>> = {
  console: set(
    'auth.loginCustomer', 'auth.register', 'auth.logout',
    'account.profile', 'account.update', 'account.addresses.add', 'account.addresses.update', 'account.addresses.remove',
    'wallet.get', 'wallet.transactions', 'wallet.createTopUp', 'wallet.getTopUp', 'wallet.cancelTopUp', 'wallet.simulatePay',
    'plans.list', 'plans.get', 'plans.createDraft', 'plans.saveDraft', 'plans.computeOptions', 'plans.chooseOption', 'plans.confirm',
    'plans.cancel', 'plans.reschedule', 'plans.remove',
    'notifications.list', 'notifications.markRead',
    'settings.get',
    'ai.readImage', 'ai.parseSpeech', 'ai.parseIntent', 'ai.transcribe', 'ai.copilot', 'ai.explainOptions',
  ),
  backoffice: set(
    'auth.loginAdmin', 'auth.logout',
    'fleet.vehicles.list', 'fleet.vehicles.create', 'fleet.vehicles.update', 'fleet.vehicles.remove',
    'fleet.drivers.list', 'fleet.drivers.create', 'fleet.drivers.update',
    'plans.list', 'plans.get', 'plans.cancel',
    'dispatch.suggest', 'dispatch.assign', 'dispatch.unassign', 'dispatch.autoAssign',
    'users.list', 'users.get', 'users.setBlocked',
    'finance.overview', 'finance.topUps', 'finance.transactions',
    'notifications.list', 'notifications.markRead',
    'settings.get', 'settings.update',
    'live.routes', 'live.idleDrivers',
    'ai.copilot', 'ai.insights',
  ),
  driver: set(
    'auth.loginDriver', 'auth.logout',
    'driver.me', 'driver.setOnline', 'driver.routes', 'driver.startRoute', 'driver.arrive', 'driver.complete', 'driver.reportIssue',
    'driver.history', 'driver.earnings', 'driver.ping',
    'notifications.list', 'notifications.markRead',
    'settings.get',
  ),
}

/**
 * Metode AI yang punya cadangan di klien (mesin deterministik di local.ts): bila backend menjawab dengan galat
 * "layanan mati" (`jaringan` atau `layanan`), wizard tetap jalan. Penolakan (`ditolak`, mis. dimatikan admin) dan galat
 * validasi TIDAK diulang di klien. `ai.transcribe`, `ai.copilot`, `ai.insights`, dan `ai.explainOptions` tidak punya cadangan.
 */
export const LOCAL_FALLBACK: ReadonlySet<string> = set('ai.readImage', 'ai.parseSpeech', 'ai.parseIntent')

/** Aksi yang memindahkan uang: backend meminta Idempotency-Key supaya percobaan ulang tidak menagih dua kali. */
export const IDEMPOTENT: ReadonlySet<string> = set('plans.confirm', 'wallet.createTopUp')

/** Metode yang menghasilkan sesi (dan token) baru. */
export const SIGN_IN: ReadonlySet<string> = set('auth.loginCustomer', 'auth.register', 'auth.loginAdmin', 'auth.loginDriver')

/** Badan permintaan dari argumen posisi: objek bernama; argumen kosong di ujung dihilangkan. */
export function toBody(method: string, args: readonly unknown[]): Record<string, unknown> {
  const names = ARG_NAMES[method] ?? []
  const body: Record<string, unknown> = {}
  names.forEach((name, i) => {
    const value = args[i]
    if (name.startsWith('...')) {
      // objek opsi disebar; kolom yang tidak diisi dihilangkan
      if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
        for (const [key, v] of Object.entries(value)) if (v !== undefined) body[key] = v
      }
      return
    }
    if (value !== undefined) body[name] = value
  })
  for (const [key, v] of Object.entries(BODY_DEFAULTS[method] ?? {})) if (!(key in body)) body[key] = v
  return body
}
