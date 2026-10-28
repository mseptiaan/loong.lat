import { afterEach, describe, expect, it } from 'vitest'
import {
  DEFAULT_LOCALE,
  catalogProblems,
  enShared,
  formatAgo,
  formatDate,
  formatDateLong,
  formatDateTime,
  formatDuration,
  formatKg,
  formatKm,
  formatList,
  formatPercent,
  formatRelativeDay,
  formatRupiah,
  formatTime,
  getLocale,
  idShared,
  msg,
  registerMessages,
  renderMsg,
  setLocale,
  subscribeLocale,
  t,
  tFor,
  translate,
  translator,
  type Messages,
  type Translation,
} from './index'

afterEach(() => setLocale(DEFAULT_LOCALE))

describe('bahasa aktif', () => {
  it('bawaan Inggris dan berganti lewat setLocale', () => {
    expect(getLocale()).toBe('en')
    let calls = 0
    const off = subscribeLocale(() => calls++)
    setLocale('id')
    expect(getLocale()).toBe('id')
    setLocale('id')
    expect(calls).toBe(1)
    off()
    setLocale('en')
    expect(calls).toBe(1)
  })
})

const enTest = {
  'test.hello': 'Hello, {name}!',
  'test.price': 'Total {amount|rupiah} for {km|km}',
  'test.stops_one': '{count} stop',
  'test.stops_other': '{count} stops',
  'test.only': 'Only in English',
  'test.nested': 'Driver: {who}',
  'test.link': 'Need help? <link>Contact us</link>',
} as const
const idTest: Translation<typeof enTest> = {
  'test.hello': 'Halo, {name}!',
  'test.price': 'Total {amount|rupiah} untuk {km|km}',
  'test.stops_other': '{count} stop',
  'test.only': 'Hanya di Inggris',
  'test.nested': 'Driver: {who}',
  'test.link': 'Butuh bantuan? <link>Hubungi kami</link>',
}
registerMessages('en', enTest)
registerMessages('id', idTest)
const tt = translator<typeof enTest>()

describe('terjemahan', () => {
  it('mengisi parameter dan mengikuti bahasa', () => {
    expect(tt('test.hello', { name: 'Dina' })).toBe('Hello, Dina!')
    setLocale('id')
    expect(tt('test.hello', { name: 'Dina' })).toBe('Halo, Dina!')
  })

  it('memformat nilai bernama menurut bahasa', () => {
    expect(tt('test.price', { amount: 46500, km: 12.4 })).toBe('Total Rp 46,500 for 12.4 km')
    setLocale('id')
    expect(tt('test.price', { amount: 46500, km: 12.4 })).toBe('Total Rp 46.500 untuk 12,4 km')
  })

  it('memilih bentuk jamak', () => {
    expect(tt('test.stops', { count: 1 })).toBe('1 stop')
    expect(tt('test.stops', { count: 3 })).toBe('3 stops')
    expect(tt('test.stops', { count: 0 })).toBe('0 stops')
    setLocale('id')
    expect(tt('test.stops', { count: 1 })).toBe('1 stop')
    expect(tt('test.stops', { count: 3 })).toBe('3 stop')
  })

  it('merender pesan bersarang dan Msg netral-bahasa', () => {
    const who = { key: 'test.hello', params: { name: 'Budi' } }
    expect(tt('test.nested', { who })).toBe('Driver: Hello, Budi!')
    const stored = msg('fmt.duration.hourMin', { h: 1, m: '05' })
    expect(renderMsg(stored)).toBe('1 h 05 min')
    setLocale('id')
    expect(renderMsg(stored)).toBe('1 j 05 mnt')
    expect(renderMsg(stored, 'en')).toBe('1 h 05 min')
  })

  it('tFor terikat pada bahasa yang diminta, tidak pada bahasa aktif', () => {
    setLocale('en')
    expect(tFor('id')('fmt.ago.now')).toBe('baru saja')
    expect(t('fmt.ago.now')).toBe('just now')
  })

  it('kunci tanpa terjemahan jatuh ke Inggris, lalu ke kuncinya', () => {
    registerMessages('en', { 'test.fallback': 'English only' })
    setLocale('id')
    expect(translate('id', 'test.fallback')).toBe('English only')
    expect(translate('id', 'test.tidak.ada')).toBe('test.tidak.ada')
  })

  it('tipe: kunci, parameter, dan tag yang salah ditolak saat typecheck', () => {
    // @ts-expect-error kunci tidak ada
    void (() => tt('test.missing'))
    // @ts-expect-error parameter wajib
    void (() => tt('test.hello'))
    // @ts-expect-error nama parameter salah
    void (() => tt('test.hello', { nama: 'x' }))
    // @ts-expect-error kunci jamak butuh count
    void (() => tt('test.stops', { n: 2 }))
    expect(true).toBe(true)
  })
})

describe('format', () => {
  const at = '2026-10-21T07:20:00.000Z' // 14.20 WIB

  it('rupiah, jarak, berat, persen', () => {
    expect(formatRupiah(46500, 'en')).toBe('Rp 46,500')
    expect(formatRupiah(46500, 'id')).toBe('Rp 46.500')
    expect(formatKm(1.84, 'en')).toBe('1.8 km')
    expect(formatKm(12.44, 'id')).toBe('12,4 km')
    expect(formatKg(2, 'id')).toBe('2 kg')
    expect(formatKg(4.5, 'id')).toBe('4,5 kg')
    expect(formatKg(12.4, 'en')).toBe('12 kg')
    expect(formatPercent(22, 'en')).toBe('22%')
  })

  it('durasi', () => {
    expect(formatDuration(52, 'en')).toBe('52 min')
    expect(formatDuration(65, 'en')).toBe('1 h 05 min')
    expect(formatDuration(120, 'en')).toBe('2 h')
    expect(formatDuration(65, 'id')).toBe('1 j 05 mnt')
  })

  it('jam dan tanggal (WIB)', () => {
    expect(formatTime(at, 'en')).toBe('14:20')
    expect(formatTime(at, 'id')).toBe('14.20')
    expect(formatDate(at, 'en')).toBe('21 Oct')
    expect(formatDate(at, 'id')).toBe('21 Okt')
    expect(formatDateTime(at, 'en')).toBe('21 Oct, 14:20')
    expect(formatDateLong(at, 'en')).toMatch(/Wednesday,? 21 October 2026/)
    expect(formatDateLong(at, 'id')).toMatch(/Rabu,? 21 Oktober 2026/)
  })

  it('tengah malam tampil 00, bukan 24', () => {
    expect(formatTime('2026-10-20T17:05:00.000Z', 'en')).toBe('00:05')
  })

  it('hari relatif dan "lalu"', () => {
    const now = new Date(at)
    expect(formatRelativeDay(at, now, 'en')).toBe('Today, 14:20')
    expect(formatRelativeDay('2026-10-22T02:00:00.000Z', now, 'id')).toBe('Besok, 09.00')
    expect(formatRelativeDay('2026-10-25T02:00:00.000Z', now, 'en')).toBe('25 Oct, 09:00')
    const ago = (sec: number, l: 'en' | 'id') => formatAgo(now.getTime() - sec * 1000, now, l)
    expect(ago(3, 'en')).toBe('just now')
    expect(ago(125, 'en')).toBe('2 min ago')
    expect(ago(125, 'id')).toBe('2 mnt lalu')
    expect(ago(3 * 3600, 'en')).toBe('3 h ago')
    expect(ago(86_400, 'en')).toBe('yesterday')
    expect(ago(3 * 86_400, 'en')).toBe('3 days ago')
    expect(ago(3 * 86_400, 'id')).toBe('3 hari lalu')
  })

  it('daftar', () => {
    expect(formatList(['A', 'B', 'C'], 'en')).toBe('A, B and C')
    expect(formatList(['A', 'B', 'C'], 'id')).toBe('A, B, dan C')
  })
})

describe('katalog bersama', () => {
  it('Inggris dan Indonesia konsisten', () => {
    expect(catalogProblems(enShared, idShared)).toEqual([])
  })

  it('catalogProblems menangkap kunci, parameter, dan tag yang tidak cocok', () => {
    const en: Messages = { a: 'Hi {x}', b: 'Go <l>here</l>', c: 'Only en', d_one: '{count} item', d_other: '{count} items' }
    const id: Messages = { a: 'Hai {y}', b: 'Ke sini', d_other: '{count} barang', e: 'Extra' }
    expect(catalogProblems(en, id)).toEqual([
      'a: parameter berbeda ({x} vs {y})',
      'b: tag berbeda (<l> vs <>)',
      'c: belum ada di katalog Indonesia',
      'e: hanya ada di katalog Indonesia',
    ])
  })

  it('label domain tidak bocor sebagai kunci mentah', () => {
    for (const locale of ['en', 'id'] as const) {
      expect(translate(locale, 'dom.planStatus.menunggu_driver')).not.toMatch(/^dom\./)
      expect(translate(locale, 'cat.kulkas-mini')).not.toMatch(/^cat\./)
    }
  })
})
