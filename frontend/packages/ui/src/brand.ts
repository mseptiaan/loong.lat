/**
 * Identitas visual loong.lat: satu sumber geometri untuk komponen `Logo`, berkas di `brand/`, favicon, dan ikon PWA.
 * `npm run build:brand` menulis ulang berkas-berkasnya; `brand.test.ts` menjaga agar berkas tidak melenceng dari sini.
 *
 * Tanda: dua cincin saling mengait ("oo" dari loong) di atas kotak aksen; rute yang menyambung dua titik.
 * Wordmark: huruf monoline geometris (garis 5, tinggi-x 20) yang digambar sebagai path, jadi tidak bergantung pada font.
 * ".lat" memakai aksen, dan titiknya berupa kotak membulat, seperti penanda "antar" di peta.
 */

/** Nilai heksa mengikuti token: `--sea-500`, `--gray-9`, `--gray-0` (tema terang). File SVG mandiri tidak bisa membaca CSS. */
export const BRAND = { name: 'loong.lat', accent: '#00618f', ink: '#201914', onAccent: '#fbfaf7' } as const

const r2 = (n: number) => Math.round(n * 100) / 100
const rad = (deg: number) => (deg * Math.PI) / 180
const deg = (radians: number) => (radians * 180) / Math.PI

/* ---------------------------------- Tanda (kotak 64 x 64) ---------------------------------- */

export const MARK_SIZE = 64
/** Radius sudut kotak aksen (ikon PWA memakai 0 agar bisa di-mask OS). */
export const MARK_RADIUS = 18

const RING = { r: 13, halfGap: 8, stroke: 5, knockout: 9 }
const CY = MARK_SIZE / 2
const CX_A = CY - RING.halfGap
const CX_B = CY + RING.halfGap

const ringPath = (cx: number) => `M${cx - RING.r} ${CY}a${RING.r} ${RING.r} 0 1 0 ${2 * RING.r} 0a${RING.r} ${RING.r} 0 1 0 ${-2 * RING.r} 0`
const arcPoint = (cx: number, angle: number) => `${r2(cx + RING.r * Math.cos(rad(angle)))} ${r2(CY + RING.r * Math.sin(rad(angle)))}`
/** Busur +-`span` derajat di sekitar titik silang, searah jarum jam. */
const overArc = (cx: number, crossing: number, span: number) =>
  `M${arcPoint(cx, crossing - span)}A${RING.r} ${RING.r} 0 0 1 ${arcPoint(cx, crossing + span)}`

const crossingY = Math.sqrt(RING.r ** 2 - RING.halfGap ** 2)
// Cincin B melintas di atas A pada titik silang atas, A di atas B pada titik silang bawah: jalinan rantai.
const TOP = deg(Math.atan2(-crossingY, -RING.halfGap))
const BOTTOM = deg(Math.atan2(crossingY, RING.halfGap))
// Untaian atas digambar sedikit lebih panjang dari celahnya, supaya ujungnya jatuh di atas cincin yang sudah putih
// dan tidak meninggalkan garis halus akibat anti-aliasing.
const GAP_SPAN = 24
const OVER_SPAN = 28

export type MarkStroke = { d: string; /** `fg` = warna cincin, `tile` = warna kotak (celah di bawah untaian atas) */ tone: 'fg' | 'tile'; width: number }

export const MARK_STROKES: readonly MarkStroke[] = [
  { d: ringPath(CX_A), tone: 'fg', width: RING.stroke },
  { d: ringPath(CX_B), tone: 'fg', width: RING.stroke },
  { d: overArc(CX_B, TOP, GAP_SPAN), tone: 'tile', width: RING.knockout },
  { d: overArc(CX_B, TOP, OVER_SPAN), tone: 'fg', width: RING.stroke },
  { d: overArc(CX_A, BOTTOM, GAP_SPAN), tone: 'tile', width: RING.knockout },
  { d: overArc(CX_A, BOTTOM, OVER_SPAN), tone: 'fg', width: RING.stroke },
]

/* ------------------------------ Wordmark "loong.lat" (monoline) ------------------------------ */

export const WORDMARK_STROKE = 5
const BOWL = 10 // radius bowl huruf bulat (garis tengah); tinggi-x garis tengah = 2 x BOWL
const LETTER_GAP = 6
const HALF = WORDMARK_STROKE / 2
const BASE = 40 // garis dasar (garis tengah)
const XTOP = 20 // puncak tinggi-x (garis tengah)
const MID = (BASE + XTOP) / 2

/** Pusat vertikal tinggi-x, dipakai untuk menyejajarkan wordmark dengan pusat tanda. */
export const WORDMARK_X_CENTER = MID
const OUTER_BASE = BASE + HALF
const DOT = 6.5

const bowl = (c: number) => `M${r2(c + BOWL)} ${MID}A${BOWL} ${BOWL} 0 1 1 ${r2(c - BOWL)} ${MID}A${BOWL} ${BOWL} 0 1 1 ${r2(c + BOWL)} ${MID}`
const round = (stem: (c: number) => string) => ({
  width: 2 * BOWL + WORDMARK_STROKE,
  d: (x: number) => {
    const c = x + HALF + BOWL
    return bowl(c) + stem(c)
  },
})

const GLYPHS: Record<string, { width: number; d: (x: number) => string }> = {
  l: { width: WORDMARK_STROKE, d: (x) => `M${r2(x + HALF)} 7V${BASE}` },
  o: round(() => ''),
  a: round((c) => `M${r2(c + BOWL)} ${XTOP}V${BASE}`),
  g: round((c) => `M${r2(c + BOWL)} ${XTOP}V42A${BOWL} ${BOWL} 0 0 1 ${r2(c)} 52H${r2(c - 5)}`),
  n: {
    width: 2 * BOWL + WORDMARK_STROKE,
    d: (x) => {
      const a = x + HALF
      return `M${r2(a)} ${XTOP}V${BASE}M${r2(a)} ${MID}A${BOWL} ${BOWL} 0 0 1 ${r2(a + 2 * BOWL)} ${MID}V${BASE}`
    },
  },
  t: {
    width: 20,
    d: (x) => {
      const s = x + HALF + 6
      return `M${r2(s)} 12V34A6 6 0 0 0 ${r2(s + 6)} ${BASE}H${r2(s + 8)}M${r2(s - 6)} ${XTOP}H${r2(s + 9)}`
    },
  },
}

function layout(text: string) {
  let x = 0
  const parts: { ch: string; x: number; d: string }[] = []
  for (const ch of text) {
    const width = ch === '.' ? DOT : (GLYPHS[ch]?.width ?? 0)
    parts.push({ ch, x, d: ch === '.' ? '' : (GLYPHS[ch]?.d(x) ?? '') })
    x += width + LETTER_GAP
  }
  return { parts, width: x - LETTER_GAP }
}

const placed = layout('loong.lat')
const dotAt = placed.parts.find((p) => p.ch === '.')?.x ?? 0
const dotIndex = placed.parts.findIndex((p) => p.ch === '.')

export const WORDMARK = {
  /** Lebar luar wordmark (tepi kiri huruf pertama sampai tepi kanan huruf terakhir). */
  width: r2(placed.width),
  loong: placed.parts.slice(0, dotIndex).map((p) => p.d).join(''),
  lat: placed.parts.slice(dotIndex + 1).map((p) => p.d).join(''),
  dot: { x: r2(dotAt), y: r2(OUTER_BASE - DOT), size: DOT, radius: 1.8 },
}

/* ------------------------------- Lockup: tanda + wordmark ------------------------------- */

const LOCKUP_GAP = 14
const LOCKUP_SCALE = 1.06

export const LOCKUP = {
  height: MARK_SIZE,
  width: Math.ceil((MARK_SIZE + LOCKUP_GAP + WORDMARK.width * LOCKUP_SCALE) * 10) / 10,
  /** Geser dan skala yang dipakai pada grup wordmark; pusat tinggi-x jatuh di pusat tanda. */
  wordmarkTransform: `translate(${MARK_SIZE + LOCKUP_GAP} ${r2(CY - WORDMARK_X_CENTER * LOCKUP_SCALE)}) scale(${LOCKUP_SCALE})`,
}

/* ------------------------------- Berkas SVG mandiri ------------------------------- */

const SVG_NS = 'http://www.w3.org/2000/svg'

const markBody = (radius: number) => [
  `<rect width="${MARK_SIZE}" height="${MARK_SIZE}"${radius ? ` rx="${radius}"` : ''} fill="${BRAND.accent}"/>`,
  `<g fill="none">`,
  ...MARK_STROKES.map((s) => `  <path d="${s.d}" stroke="${s.tone === 'fg' ? BRAND.onAccent : BRAND.accent}" stroke-width="${s.width}"/>`),
  `</g>`,
]

/** Tanda saja. `bleed` = kotak penuh tanpa sudut membulat, untuk ikon PWA yang di-mask oleh OS. */
export function markSvg({ bleed = false }: { bleed?: boolean } = {}): string {
  return [
    `<svg xmlns="${SVG_NS}" viewBox="0 0 ${MARK_SIZE} ${MARK_SIZE}" role="img" aria-label="${BRAND.name}">`,
    `<title>${BRAND.name}</title>`,
    ...markBody(bleed ? 0 : MARK_RADIUS),
    `</svg>`,
    '',
  ].join('\n')
}

/** Logo utama: tanda + wordmark, untuk latar terang. */
export function lockupSvg(): string {
  return [
    `<svg xmlns="${SVG_NS}" viewBox="0 0 ${LOCKUP.width} ${LOCKUP.height}" role="img" aria-label="${BRAND.name}">`,
    `<title>${BRAND.name}</title>`,
    ...markBody(MARK_RADIUS),
    `<g transform="${LOCKUP.wordmarkTransform}" fill="none" stroke-width="${WORDMARK_STROKE}" stroke-linecap="round" stroke-linejoin="round">`,
    `  <path d="${WORDMARK.loong}" stroke="${BRAND.ink}"/>`,
    `  <path d="${WORDMARK.lat}" stroke="${BRAND.accent}"/>`,
    `  <rect x="${WORDMARK.dot.x}" y="${WORDMARK.dot.y}" width="${WORDMARK.dot.size}" height="${WORDMARK.dot.size}" rx="${WORDMARK.dot.radius}" fill="${BRAND.accent}"/>`,
    `</g>`,
    `</svg>`,
    '',
  ].join('\n')
}
