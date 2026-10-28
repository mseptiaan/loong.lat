<p align="center">
  <img src="frontend/brand/loong-lat.svg" alt="loong.lat" height="64">
</p>

<p align="center"><strong>Ten parcels, one route, every rupiah explained.</strong></p>

<p align="center">
  <a href="https://loong.lat">loong.lat</a> ·
  <a href="https://console.loong.lat">Sender dashboard</a> ·
  <a href="https://backoffice.loong.lat">Backoffice</a> ·
  <a href="https://driver.loong.lat">Driver app</a>
</p>

---

loong.lat is a courier platform for small senders in Greater Jakarta. It groups many parcels into the **cheapest, shortest, or fastest** route, prices every option against sending each parcel separately, and explains the choice with numbers you can trace. The route math runs in **Modal sandboxes**; every AI feature runs on **Meta's Muse models** through the Meta Model API.

Built for the Meta Global AI Developer Hackathon 2026.

## Contents

- [What it does](#what-it-does)
- [Meta models for AI](#meta-models-for-ai)
- [Modal sandboxes for the route math](#modal-sandboxes-for-the-route-math)
- [Repository layout](#repository-layout)
- [Run it locally](#run-it-locally)
- [Deploy](#deploy)
- [Tests](#tests)
- [Security notes](#security-notes)
- [License](#license)

## What it does

Three apps share one route, each on its own hostname:

| App | Who | What |
|---|---|---|
| **Sender dashboard** (`console.`) | Small online shops and individuals | A 4-step wizard: route and schedule (including a free-text "tell us what you need"), items (typed, from a photo, or spoken), pick an option on a map with an AI summary and a copilot, then review and pay from a loong.lat balance. Live tracking with proof of delivery (OTP code or photo, with an AI description of the photo). |
| **Backoffice** (`backoffice.`) | Admins and dispatchers | A command center whose queue shows what needs attention first, automatic dispatch with manual assignment as the exception, a live map, vehicles and drivers, balances and top-ups, analytics with an AI summary, and a `⌘K` command palette. |
| **Driver app** (`driver.`) | Couriers on the road | The next stop first, swipe to arrive and complete, proof by OTP or photo, report a problem. Installs as a PWA. |

The root domain serves the landing page in English and Indonesian.

Product rules that shape every screen:

- **AI suggests, people decide.** No model call can change data; every AI failure has a manual way out.
- **Every number has a source.** Prices come with a breakdown, ETAs say what they are based on, and AI answers only quote numbers returned by tools.
- **Savings are shown, not claimed.** Always in rupiah and percent against the "send separately" baseline.

## Meta models for AI

Every AI feature goes through one gateway, `backend/services/ai`. It is the only place that holds the Meta key. Callers name a **profile**; the gateway picks the model, reasoning effort, token budget, timeout, and request size, rate-limits each caller by token, and **never logs message, image, or audio content** (only sizes, token counts, and latency).

| Model | Profile | What it does | Where |
|---|---|---|---|
| Muse Spark | `vision` | Reads a photo of the items and lists them with weights and sizes | Sender, items step |
| Muse Voice Transcribe | `transcribe` | Turns a spoken order (16 kHz mono WAV) into text the sender checks before it is parsed | Sender, items step |
| Muse Spark | `intent` | Turns "three boxes to Bekasi before noon" into stops and deadlines | Sender, route step |
| Muse Spark | `report` | Explains the computed options in plain words; the text is checked against server-built facts before it is shown | Sender, options step |
| Muse Spark | `copilot` | Answers questions with read-only tools over the sender's own plans, or over operations for admins; every tool call is visible | Sender and backoffice |
| Muse Spark | `review` | Reviews candidate routes between search rounds of a "thorough with AI review" quote and asks for more attempts | Optimizer |
| Muse Spark | `vision` | Describes proof-of-delivery photos without identifying anyone, stored in English and Indonesian | Sender, tracking |
| Muse Spark | `insights` | Summarizes operations and points dispatchers at anomalies | Backoffice analytics |

Guardrails: tools are read-only, arguments are validated, the agent loop has a step limit (`backend/pkg/agent`), user-written text is treated as data rather than instructions, and numbers in an answer must come from a tool result or the server's facts.

Without `META_API_KEY` the gateway still starts and answers every AI call with 503; the apps then switch their AI features off instead of offering something that will fail.

## Modal sandboxes for the route math

Every quote is solved by the Go engine inside a **Modal sandbox**:

- **Network off, no secrets inside.** Sandboxes are created with `BlockNetwork: true`; the solver binary is mounted read-only from a private Modal Volume (`loong-solver`).
- **Numbers, never people.** A job carries only coordinates, a road distance table, parcel weights and sizes, the fleet, and the presets. No names, phone numbers, street addresses, or payment details leave our servers.

  ```json
  {
    "points": [{ "lat": -6.22664, "lng": 106.63553 }, …],
    "distM": [0, 14230, 9870, …],
    "shipments": [{ "pickup": 0, "dropoff": 3, "weightKg": 4.6 }, …],
    "presets": ["termurah", "terpendek", "tercepat", "seimbang"]
  }
  ```

- **Deterministic.** The same job gives the same answer, so options are cached by input hash and can be checked again later.
- **Fast and cheap.** A warm pool (`MODAL_POOL_SIZE`, default 2) keeps sandboxes ready. Measured on our Modal account:

  | | Time |
  |---|---|
  | Cold sandbox, one job | about 4.5 s |
  | Warm sandbox, one job | about 1.0 s (p95 1.25 s) |
  | Full quote with Mapbox, warm | about 1.1 to 1.25 s |
  | Cost per warm job (2 cores, 1 GiB) | about US$0.00009 |

- **Versioned.** The solver and the optimizer must report the same engine version; a mismatched job is rejected (`engine_mismatch`). If a sandbox cannot start, the optimizer falls back to the same engine in process (`OPTIMIZER_FALLBACK=none` turns that off).

Publish a new solver after changing the engine:

```bash
cd backend
set -a && . ./.env && set +a          # MODAL_TOKEN_ID, MODAL_TOKEN_SECRET
scripts/publish-solver.sh              # builds linux/amd64, uploads solver-<version>, verifies it in a fresh sandbox
# then build the optimizer image with the same version (VERSION build arg) and set MODAL_SOLVER_VERSION
```

Measure latency, concurrency, and cost at any time with `go run ./services/optimizer/cmd/modalprobe -n 10`.

## Repository layout

```
backend/
  pkg/          engine, contracts, optimizerapi, meta (Meta Model API client), aiapi, agent, notices, platform, eventbus, routeplan
  services/     console, backoffice, driver, optimizer, ai (each with its own Dockerfile and compose.yaml)
  cmd/topics/   creates the Kafka topics and their dead-letter topics
  deploy/       local Postgres + Redpanda, solver image
  e2e/          end-to-end tests: three APIs + optimizer + Kafka + Postgres in one process
frontend/
  landing/      the landing page (root domain)
  dashboard/    sender dashboard (console.)
  backoffice/   backoffice (backoffice.)
  mobile/       driver PWA (driver.)
  api/          mock backend host (api.), used only in mock mode
  packages/     ui, domain, i18n, data, shell, http-api, mock-api, gazetteer
  deploy/web/   nginx image for the four static sites
deploy/server/  production compose file and .env template
```

`backend/README.md` and `frontend/README.md` go deeper into each side (in Indonesian).

## Run it locally

Requirements: Node 22+, Go 1.27, Docker.

**Frontend only (everything in the browser, no keys needed):**

```bash
cd frontend
npm install
npm run dev            # gateway + dev servers
```

Open http://loong.localhost (if port 80 is taken: http://loong.localhost:4000). `*.localhost` resolves to your machine and counts as a secure context, so the microphone, GPS, and PWA work. Add `?demo` to any app address for the demo panel (speed up time, simulate failures, reset data).

**With the Go backend:**

```bash
cd backend
cp .env.example .env                  # optional: META_API_KEY, MODAL_*, MAPBOX_TOKEN
scripts/dev-up.sh                     # Postgres (55432) + Redpanda (19092), creates the topics
ADMIN_EMAIL=admin@loong.test ADMIN_PASSWORD=<dev-password> SIMULATE_PAYMENTS=true \
  sh -c 'for s in optimizer ai console backoffice driver; do docker compose -f services/$s/compose.yaml up -d --build --wait; done'

cd ../frontend
npm run dev:http                      # the apps call the APIs on :8083, :8082, :8081
```

By default the optimizer solves in process and estimates road distances (straight line × 1.28). Set `RUNNER=modal` and the Modal and Mapbox keys to use sandboxes and real roads.

## Deploy

Production runs on a single ARM64 server behind the server's existing Traefik, with DNS and TLS at Cloudflare. All images live in the public Docker Hub repository `mseptiaan/loong.lat`, one tag per component.

| Host | Container | Port |
|---|---|---|
| `loong.lat`, `console.`, `backoffice.`, `driver.loong.lat` | `web` (nginx serves the four static sites by hostname) | 80 |
| `api-console.loong.lat` | `console-api` | 8083 |
| `api-backoffice.loong.lat` | `backoffice-api` | 8082 |
| `api-driver.loong.lat` | `driver-api` | 8081 |
| internal only | `optimizer`, `ai`, `postgres`, `redpanda`, `topics` (one-shot) | none |

### 1. Build and push the images

```bash
# backend images (linux/arm64): one tag per service, plus a dated tag
cd backend
for s in console backoffice driver ai; do
  docker buildx build --platform linux/arm64 -f services/$s/Dockerfile \
    -t mseptiaan/loong.lat:$s$( [ $s = ai ] || echo -api ) --push .
done
docker buildx build --platform linux/arm64 -f services/optimizer/Dockerfile \
  --build-arg VERSION=<solver version from publish-solver.sh> -t mseptiaan/loong.lat:optimizer --push .
docker buildx build --platform linux/arm64 -f cmd/topics/Dockerfile -t mseptiaan/loong.lat:topics --push .

# the four static sites in one nginx image; the apps are built in http mode against api-<app>.loong.lat
cd ../frontend
deploy/web/build.sh
```

`deploy/web/build.sh` builds the landing page with `landing/.env.production` (production app URLs and the live-backend copy), builds the three apps with `VITE_API_MODE=http`, `VITE_API_URL=https://api-<app>.loong.lat`, and `VITE_SIMULATE_PAYMENTS=1`, then pushes `mseptiaan/loong.lat:web`. Set `NO_PUSH=1` to build locally only.

### 2. Run the stack on the server

```bash
# from your machine: the compose file and the Postgres init script
ssh ubuntu@<server> 'mkdir -p ~/loong/postgres'
scp deploy/server/docker-compose.yml ubuntu@<server>:~/loong/
scp backend/deploy/postgres/init.sh ubuntu@<server>:~/loong/postgres/

# on the server: create ~/loong/.env from deploy/server/.env.example (chmod 600), then
ssh ubuntu@<server>
cd ~/loong
docker compose pull
docker compose up -d                    # project "loong": separate from every other stack on the server
docker compose ps
```

The compose file:

- joins only `web` and the three APIs to Traefik's network (`ubuntu_default`) and routes them with labels: `Host(...)` rules, the `web` and `websecure` entrypoints, and the `leresolver` certificate resolver;
- keeps the optimizer, the AI gateway, Postgres, and Redpanda on a private network with no published ports;
- runs `topics` once before the APIs start, and the APIs migrate their own databases on start;
- creates the first backoffice admin from `ADMIN_EMAIL`/`ADMIN_PASSWORD` and, with `SEED_DEMO=true`, two demo drivers.

### 3. Update

```bash
# after pushing new images
cd ~/loong && docker compose pull && docker compose up -d
```

If the server's Docker Hub login is stale, pull anonymously (the repository is public): `DOCKER_CONFIG=$(mktemp -d) docker compose pull`.

### Configuration

| Variable | Used by | Purpose |
|---|---|---|
| `DOMAIN` | compose | Root domain for all routing rules and CORS origins (`loong.lat`) |
| `POSTGRES_PASSWORD`, `DB_PASSWORD` | postgres, APIs | Superuser password, and the password of each service's role |
| `OPTIMIZER_TOKEN` | console-api, optimizer | Shared bearer token |
| `AI_TOKEN_CONSOLE`, `AI_TOKEN_BACKOFFICE`, `AI_TOKEN_OPTIMIZER` | ai and its callers | One bearer token per caller |
| `META_API_KEY`, `META_BASE_URL`, `META_MODEL` | ai | Meta Model API |
| `RUNNER`, `MODAL_TOKEN_ID`, `MODAL_TOKEN_SECRET`, `MODAL_VOLUME`, `MODAL_SOLVER_VERSION` | optimizer | Modal sandboxes |
| `MAPBOX_TOKEN` | optimizer | Toll-free road distances and route lines |
| `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `SEED_DEMO` | backoffice-api | First admin and demo drivers |
| `IMAGE_SUFFIX` | compose | Pin a dated tag, for example `-20261027` |

## Tests

```bash
# backend
cd backend
go vet ./... && go test -race ./...
LOONG_E2E=1 go test -tags e2e -count=1 ./e2e        # needs scripts/dev-up.sh

# frontend
cd frontend
npm run typecheck && npm run lint && npm test
npm run check:i18n && npm run check:contrast
```

Property tests compare the heuristic engine with the exact solver on random instances; parity tests keep the browser engine and the Go engine honest with each other.

## Security notes

- Meta, Modal, and Mapbox keys live only in `.env` files (git-ignored) or the server's environment, never in the repository, a frontend bundle, or a log. Images are built with a `.dockerignore` that excludes every `.env`.
- The AI gateway is the only holder of the Meta key and never logs prompt or media content.
- Solver sandboxes have no network access and receive no personal data.
- The public demo uses shared accounts and `SIMULATE_PAYMENTS=true` (top-ups can be credited without paying). Turn both off for anything beyond a demo.

## License

Licensed under the [Apache License, Version 2.0](LICENSE). See [NOTICE](NOTICE) for attribution.

The Meta and Modal logos are trademarks of their respective owners and are not covered by this license. The built-in place list (`frontend/packages/gazetteer/src/places.ts`) contains data © OpenStreetMap contributors under the [Open Database License (ODbL)](https://opendatacommons.org/licenses/odbl/1-0/).
