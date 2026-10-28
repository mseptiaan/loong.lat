import { useMemo } from 'react'
import { t } from '../i18n'

const N = 25

function hash(input: string): number {
  let h = 2166136261
  for (let i = 0; i < input.length; i++) {
    h ^= input.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return h >>> 0
}

/** Matriks N x N deterministik dari teks: tiga pola sudut seperti QR sungguhan, isinya acak-tetap. */
function matrix(value: string): boolean[][] {
  let s = hash(value) || 1
  const rand = () => {
    s ^= s << 13
    s >>>= 0
    s ^= s >>> 17
    s ^= s << 5
    s >>>= 0
    return s / 2 ** 32
  }
  const m = Array.from({ length: N }, () => Array.from({ length: N }, () => rand() > 0.52))
  const finder = (r0: number, c0: number) => {
    for (let r = -1; r <= 7; r++) {
      for (let c = -1; c <= 7; c++) {
        const rr = r0 + r
        const cc = c0 + c
        if (rr < 0 || cc < 0 || rr >= N || cc >= N) continue
        const ring = r === 0 || r === 6 || c === 0 || c === 6
        const core = r >= 2 && r <= 4 && c >= 2 && c <= 4
        m[rr]![cc] = r >= 0 && r <= 6 && c >= 0 && c <= 6 && (ring || core)
      }
    }
  }
  finder(0, 0)
  finder(0, N - 7)
  finder(N - 7, 0)
  return m
}

/**
 * Gambar mirip kode QR untuk top up QRIS di mock. BUKAN kode QR sungguhan dan tidak bisa dipindai; penyedia
 * pembayaran sungguhan memberi string QRIS yang dirender di sini.
 */
export function QrCode({ value, size = 184 }: { value: string; size?: number }) {
  const cells = useMemo(() => matrix(value), [value])
  return (
    <svg role="img" aria-label={t('dash.wallet.qrAlt')} width={size} height={size} viewBox={`-1 -1 ${N + 2} ${N + 2}`} shapeRendering="crispEdges" className="rounded-input bg-white p-1">
      {cells.flatMap((row, r) => row.map((on, c) => (on ? <rect key={`${r}-${c}`} x={c} y={r} width="1" height="1" fill="#1b1814" /> : null)))}
    </svg>
  )
}
