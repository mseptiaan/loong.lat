import { virtualNowOf, type AppEvent, type AppNotification, type NotificationAudience } from '@loong/domain'
import type { Msg } from '@loong/i18n'
import { DB_VERSION, type DB } from '../db'
import { createSeed } from '../seed'
import type { RoadProvider } from '../roads/provider'

/* ---------- penyimpanan ---------- */

export interface RawStorage {
  read(): string | null
  write(raw: string): void
}

export const STORAGE_KEY = 'loong:db:v1'

export function localRawStorage(key = STORAGE_KEY): RawStorage {
  return {
    read: () => globalThis.localStorage.getItem(key),
    write: (raw) => globalThis.localStorage.setItem(key, raw),
  }
}

export function memoryRawStorage(): RawStorage {
  let raw: string | null = null
  return { read: () => raw, write: (v) => void (raw = v) }
}

/* ---------- opsi ---------- */

export interface ServerOptions {
  storage: RawStorage
  /** Jeda tiruan jaringan dalam ms (acak antara min-max). [0,0] untuk tes. */
  latency?: readonly [number, number]
  /** Waktu nyata; bisa diganti di tes. */
  realNow?: () => number
  /**
   * Data jalan sungguhan (alat bantu; optimasi tetap engine kita). Kosong = jarak perkiraan dan jalur garis halus,
   * yaitu perilaku bawaan di tes dan saat tidak ada token.
   */
  roads?: RoadProvider
}

/**
 * "Server" tiruan: memegang DB di penyimpanan, jam virtual, dan pemancar peristiwa.
 * Satu instance per halaman api.<domain>; beberapa instance (tab berbeda) berbagi penyimpanan yang sama.
 */
export class MockServer {
  readonly options: Required<Pick<ServerOptions, 'latency'>> & ServerOptions
  private cache: { raw: string; db: DB } | null = null
  private listeners = new Set<(e: AppEvent) => void>()
  /** Diisi oleh host untuk meneruskan peristiwa ke tab lain (BroadcastChannel). */
  relay?: (e: AppEvent) => void

  constructor(options: ServerOptions) {
    this.options = { latency: [80, 240], ...options }
  }

  /* --- waktu --- */

  realNow(): number {
    return this.options.realNow?.() ?? Date.now()
  }

  /** Waktu virtual sekarang (ms). */
  now(): number {
    return virtualNowOf(this.db().clock, this.realNow())
  }

  nowIso(): string {
    return new Date(this.now()).toISOString()
  }

  async delay(): Promise<void> {
    const [min, max] = this.options.latency
    if (max <= 0) return
    await sleep(min + Math.random() * (max - min))
  }

  /* --- penyimpanan --- */

  /** Baca DB (membuat seed bila belum ada atau versi usang). Hasilnya tidak boleh diubah di luar `mutate`. */
  db(): DB {
    const raw = this.options.storage.read()
    if (raw && this.cache && this.cache.raw === raw) return this.cache.db
    if (raw) {
      try {
        const db = JSON.parse(raw) as DB
        if (db.version === DB_VERSION) {
          this.cache = { raw, db }
          return db
        }
      } catch {
        // JSON rusak: jatuh ke seed ulang
      }
    }
    return this.reseed()
  }

  private persist(db: DB): void {
    const raw = JSON.stringify(db)
    this.options.storage.write(raw)
    this.cache = { raw, db }
  }

  reseed(scenario?: string): DB {
    const db = createSeed(this.realNow(), scenario)
    this.persist(db)
    return db
  }

  /**
   * Terapkan perubahan secara atomik. Bila `fn` melempar galat, cache dibuang sehingga perubahan setengah jadi
   * tidak pernah terbaca lagi.
   */
  mutate<T>(fn: (db: DB) => T): T {
    const db = this.db()
    try {
      const result = fn(db)
      db.rev += 1
      this.persist(db)
      return result
    } catch (err) {
      this.cache = null
      throw err
    }
  }

  /* --- peristiwa --- */

  subscribe(listener: (e: AppEvent) => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  /** Pancarkan ke pelanggan lokal dan (lewat relay) ke tab lain. */
  emit(event: AppEvent): void {
    this.deliver(event)
    this.relay?.(event)
  }

  /** Dipanggil oleh host untuk peristiwa yang datang dari tab lain. */
  deliver(event: AppEvent): void {
    for (const l of this.listeners) l(event)
  }

  /* --- notifikasi --- */

  notify(db: DB, audience: NotificationAudience, title: Msg, body: Msg, planId?: string): AppNotification {
    db.counters.notification += 1
    const n: AppNotification = {
      id: `ntf-${db.counters.notification}`,
      audience,
      at: new Date(virtualNowOf(db.clock, this.realNow())).toISOString(),
      title,
      body,
      planId,
      read: false,
    }
    db.notifications.push(n)
    if (db.notifications.length > 400) db.notifications.splice(0, db.notifications.length - 400)
    return n
  }
}

export const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms))

export const clone = <T>(value: T): T => structuredClone(value)
