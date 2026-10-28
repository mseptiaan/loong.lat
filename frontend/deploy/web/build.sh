#!/usr/bin/env bash
# Membangun keempat situs untuk produksi dan mendorong satu image nginx (linux/arm64, server Oracle ARM):
#   landing                 -> build produksi (landing/.env.production: alamat aplikasi, mode live)
#   console/backoffice/driver -> mode http terhadap backend Go, tiap aplikasi ke API-nya sendiri (api-<app>.<domain>)
#
#   frontend/deploy/web/build.sh                       # push mseptiaan/loong.lat:web dan :web-<tanggal>
#   NO_PUSH=1 frontend/deploy/web/build.sh             # hanya build lokal
#
# Env: REPO (mseptiaan/loong.lat), API_URL_TEMPLATE (https://api-%s.loong.lat, %s = console|backoffice|driver), DRIVER_FALLBACK_POSITION (posisi tetap untuk
# driver tanpa GPS, mis. demo di laptop; kosongkan untuk mematikan), PLATFORM (linux/arm64).
set -euo pipefail
cd "$(dirname "$0")/../.."

REPO="${REPO:-mseptiaan/loong.lat}"
API_URL_TEMPLATE="${API_URL_TEMPLATE:-https://api-%s.loong.lat}"
DRIVER_FALLBACK_POSITION="${DRIVER_FALLBACK_POSITION--6.261,106.813}"
PLATFORM="${PLATFORM:-linux/arm64}"
STAMP="$(date +%Y%m%d%H%M)"

node scripts/sync-map-worker.mjs
npm -w @loong/landing run build
for pair in dashboard:console backoffice:backoffice mobile:driver; do
  dir="${pair%%:*}"; app="${pair##*:}"
  fallback=""
  [ "$app" = driver ] && fallback="$DRIVER_FALLBACK_POSITION"
  VITE_API_MODE=http VITE_SIMULATE_PAYMENTS=1 VITE_API_URL="$(printf "$API_URL_TEMPLATE" "$app")" VITE_DRIVER_FALLBACK_POSITION="$fallback" \
    npm -w "@loong/$dir" run build
done

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
mkdir -p "$stage/sites"
cp -R landing/dist "$stage/sites/landing"
cp -R dashboard/dist "$stage/sites/console"
cp -R backoffice/dist "$stage/sites/backoffice"
cp -R mobile/dist "$stage/sites/driver"
cp deploy/web/Dockerfile deploy/web/nginx.conf "$stage/"

out=(--push)
[ -n "${NO_PUSH:-}" ] && out=(--load)
docker buildx build --platform "$PLATFORM" "${out[@]}" -t "$REPO:web" -t "$REPO:web-$STAMP" "$stage"
echo "image: $REPO:web ($REPO:web-$STAMP)"
