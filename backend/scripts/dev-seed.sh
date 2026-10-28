#!/bin/sh
# Gives a running dev stack what a demo needs: a motorbike and a blindvan with their tariffs, one driver on the
# motorbike, and a customer with a wallet balance. The accounts are the demo accounts of frontend/README.md ("Akun
# demo"); override them with the variables below. Safe to run twice: what exists is left alone.
#
#   ADMIN_EMAIL=admin@loong.lat ADMIN_PASSWORD=... scripts/dev-seed.sh
#
# Needs the console, back office and driver APIs up (see README) and the back office started with the same
# ADMIN_EMAIL / ADMIN_PASSWORD. The top-up is paid through the simulated payment, so the console must run with
# SIMULATE_PAYMENTS=true.
set -eu

backoffice="${BACKOFFICE_URL:-http://127.0.0.1:8082}"
console="${CONSOLE_URL:-http://127.0.0.1:8083}"
admin_email="${ADMIN_EMAIL:-admin@loong.lat}"
admin_password="${ADMIN_PASSWORD:-admin1234}"
customer_email="${CUSTOMER_EMAIL:-dina@contoh.id}"
customer_password="${CUSTOMER_PASSWORD:-demo1234}"
driver_phone="${DRIVER_PHONE:-0812-3456-7801}"
driver_pin="${DRIVER_PIN:-123456}"

# rpc <base> <method> <json> [token] [idempotency-key]: prints the answer; fails on a non-2xx status.
rpc() {
  set -- "$1" "$2" "$3" "${4:-}" "${5:-}"
  out="$(mktemp)"
  code="$(curl -s -o "$out" -w '%{http_code}' -X POST "$1/v1/rpc/$2" -H 'Content-Type: application/json' \
    ${4:+-H "Authorization: Bearer $4"} ${5:+-H "Idempotency-Key: $5"} --data "$3")"
  cat "$out"; rm -f "$out"
  case "$code" in 2*) return 0 ;; *) printf '\n%s %s -> HTTP %s\n' "$1" "$2" "$code" >&2; return 1 ;; esac
}
field() { python3 -c 'import json,sys; print(json.load(sys.stdin).get(sys.argv[1], ""))' "$1"; }

token="$(rpc "$backoffice" auth.loginAdmin "{\"email\":\"$admin_email\",\"password\":\"$admin_password\"}" | field token)"
[ -n "$token" ] || { echo "admin sign-in failed: is the back office running with this ADMIN_EMAIL and ADMIN_PASSWORD?" >&2; exit 1; }

have="$(rpc "$backoffice" fleet.vehicles.list '{}' "$token")"
if [ "$(printf '%s' "$have" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')" = 0 ]; then
  motor="$(rpc "$backoffice" fleet.vehicles.create '{"input":{"type":"motor","plate":"B 1234 LNG","model":"Honda Beat","maxWeightKg":20,"maxVolumeM3":0.12,"maxDimensionCm":55,"fare":{"base":6000,"perKm":1800,"perMin":150,"perExtraStop":2500,"surchargePerKg":800,"surchargeAboveKg":10},"status":"aktif"}}' "$token" | field id)"
  rpc "$backoffice" fleet.vehicles.create '{"input":{"type":"blindvan","plate":"B 9876 LNG","model":"Daihatsu Gran Max","maxWeightKg":500,"maxVolumeM3":3.2,"maxDimensionCm":190,"fare":{"base":45000,"perKm":4500,"perMin":500,"perExtraStop":8000,"surchargePerKg":150,"surchargeAboveKg":300},"status":"aktif"}}' "$token" >/dev/null
  rpc "$backoffice" fleet.drivers.create "{\"input\":{\"name\":\"Budi Santoso\",\"phone\":\"$driver_phone\",\"vehicleId\":\"$motor\",\"pin\":\"$driver_pin\"}}" "$token" >/dev/null
  echo "created: motorbike, blindvan, driver"
else
  echo "fleet already there, left alone"
fi

session="$(rpc "$console" auth.loginCustomer "{\"email\":\"$customer_email\",\"password\":\"$customer_password\"}" 2>/dev/null || true)"
ctoken="$(printf '%s' "$session" | field token 2>/dev/null || true)"
if [ -z "$ctoken" ]; then
  ctoken="$(rpc "$console" auth.register "{\"input\":{\"name\":\"Dina Pramesti\",\"email\":\"$customer_email\",\"phone\":\"081200000001\",\"password\":\"$customer_password\"}}" | field token)"
  topup="$(rpc "$console" wallet.createTopUp '{"input":{"amountRp":500000,"method":"va_bca"}}' "$ctoken" seed-topup-1 | field id)"
  rpc "$console" wallet.simulatePay "{\"id\":\"$topup\"}" "$ctoken" >/dev/null
  echo "created: customer $customer_email with Rp 500.000"
else
  echo "customer $customer_email already there, left alone"
fi
