// Gateway dev: merutekan berdasarkan HOSTNAME (bukan folder path), meniru produksi di mana tiap app
// punya subdomain sendiri:
//
//   console.<root>     -> Dashboard pengirim (dashboard/)
//   backoffice.<root>  -> Backoffice          (backoffice/)
//   driver.<root>      -> Driver              (mobile/)
//   api.<root>         -> Backend tiruan      (api/)
//   <root> / lainnya   -> halaman depan     (landing/)
//
// <root> bebas: yang dibaca hanya label pertama hostname. Di dev pakai `loong.localhost` (resolve ke
// komputer ini tanpa ubah /etc/hosts dan dianggap konteks aman oleh Chrome, jadi mikrofon/GPS/PWA jalan).
// Di produksi domainnya loong.lat (console.loong.lat, dst.).
//
// Pemakaian:
//   node scripts/dev-gateway.mjs          -> proxy ke dev server Vite (dipanggil `npm run dev`)
//   node scripts/dev-gateway.mjs --dist   -> sajikan hasil `npm run build` dari dist/
// Env: PORT (default 80, otomatis pindah ke 4000 bila tidak bisa), HOST (default 0.0.0.0 agar port 80
// bisa dipakai tanpa root di macOS).
import http from 'node:http'
import { createReadStream } from 'node:fs'
import { stat } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import httpProxy from 'http-proxy'

const here = path.dirname(fileURLToPath(import.meta.url))
const root = path.resolve(here, '..')
const distRoot = path.join(root, 'dist')

const HOST = process.env.HOST ?? '0.0.0.0'
const serveDist = process.argv.includes('--dist')
const wantedPort = process.env.PORT ? Number(process.env.PORT) : 80
const FALLBACK_PORT = 4000

/** label pertama hostname -> aplikasi */
const APPS = {
  console: { dir: 'console', target: 'http://127.0.0.1:4171' },
  backoffice: { dir: 'backoffice', target: 'http://127.0.0.1:4172' },
  driver: { dir: 'driver', target: 'http://127.0.0.1:4173' },
  api: { dir: 'api', target: 'http://127.0.0.1:4174' },
}
/** Domain induk (dan hostname lain yang tidak dikenali): halaman depan. Hasil build-nya ada di akar dist/. */
const LANDING = { dir: '', target: 'http://127.0.0.1:4170' }

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.webmanifest': 'application/manifest+json',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.webp': 'image/webp',
  '.ico': 'image/x-icon',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2',
  '.geojson': 'application/geo+json',
  '.txt': 'text/plain; charset=utf-8',
}

function appFor(req) {
  const hostname = (req.headers.host ?? '').split(':')[0].toLowerCase()
  const labels = hostname.split('.')
  // butuh minimal 2 label ("console.localhost"); "localhost" polos = halaman depan
  return (labels.length >= 2 && APPS[labels[0]]) || LANDING
}

function send(res, status, body, type = 'text/plain; charset=utf-8') {
  res.writeHead(status, { 'content-type': type, 'cache-control': 'no-cache' })
  res.end(body)
}

async function exists(file) {
  try {
    return (await stat(file)).isFile()
  } catch {
    return false
  }
}

async function serveStatic(app, req, res) {
  const appRoot = path.join(distRoot, app.dir)
  const rel = decodeURIComponent((req.url ?? '/').split('?')[0]) || '/'
  const candidate = path.normalize(path.join(appRoot, rel))
  // cegah path traversal keluar dari folder app
  if (candidate !== appRoot && !candidate.startsWith(appRoot + path.sep)) return send(res, 403, 'Dilarang')

  let file = candidate
  if (!(await exists(file))) {
    if (path.extname(rel) !== '') return send(res, 404, 'Tidak ditemukan')
    file = path.join(appRoot, 'index.html') // fallback SPA
    if (!(await exists(file))) return send(res, 404, 'Jalankan `npm run build` dulu.')
  }
  const hashed = file.includes(`${path.sep}assets${path.sep}`)
  res.writeHead(200, {
    'content-type': MIME[path.extname(file)] ?? 'application/octet-stream',
    'cache-control': hashed ? 'public, max-age=31536000, immutable' : 'no-cache',
  })
  createReadStream(file).pipe(res)
}

const proxy = httpProxy.createProxyServer({ ws: true, changeOrigin: true })
proxy.on('error', (_err, _req, res) => {
  if (res && typeof res.writeHead === 'function' && !res.headersSent) {
    send(res, 502, 'Aplikasi belum siap. Tunggu beberapa detik lalu muat ulang halaman.')
  } else if (res && typeof res.destroy === 'function') {
    res.destroy()
  }
})

const server = http.createServer(async (req, res) => {
  try {
    const app = appFor(req)
    if (serveDist) return await serveStatic(app, req, res)
    proxy.web(req, res, { target: app.target })
  } catch (err) {
    console.error(err)
    send(res, 500, 'Kesalahan gateway')
  }
})

server.on('upgrade', (req, socket, head) => {
  if (serveDist) return socket.destroy()
  proxy.ws(req, socket, head, { target: appFor(req).target })
})

// Banner dicetak sekali, berdasarkan port yang benar-benar terikat (bukan port yang sempat dicoba).
server.on('listening', () => {
  const port = server.address().port
  const suffix = port === 80 ? '' : `:${port}`
  console.log(`\nloong.lat gateway (${serveDist ? 'dist' : 'dev'})`)
  console.log(`  landing     http://loong.localhost${suffix}`)
  for (const name of Object.keys(APPS)) {
    const label = name.padEnd(10)
    console.log(`  ${label}  http://${name}.loong.localhost${suffix}`)
  }
  console.log('')
})

function listen(port) {
  server.once('error', (err) => {
    if (port !== FALLBACK_PORT && !process.env.PORT && (err.code === 'EACCES' || err.code === 'EADDRINUSE')) {
      console.warn(`\nPort ${port} tidak bisa dipakai (${err.code}); pindah ke ${FALLBACK_PORT}.`)
      return listen(FALLBACK_PORT)
    }
    console.error(`\nGateway gagal berjalan di port ${port}: ${err.message}`)
    process.exit(1)
  })
  server.listen(port, HOST)
}

listen(wantedPort)
