#!/usr/bin/env bash
# Builds the solver for linux/amd64 and makes it available to Modal sandboxes.
#
#   scripts/publish-solver.sh                          # upload to the Modal Volume (default)
#   MODE=registry scripts/publish-solver.sh <registry>/<repo>   # push an image instead
#   NO_PUSH=1 MODE=registry scripts/publish-solver.sh loong-solver   # build the image locally only
#
# The Volume is private to your Modal workspace, so the solver (the engine's source) needs no registry; Modal
# credentials come from backend/.env, MODAL_TOKEN_ID/MODAL_TOKEN_SECRET or ~/.modal.toml. In registry mode push to a
# PRIVATE registry (docker login beforehand; this script never handles credentials) and give Modal its credentials as
# a Modal Secret (MODAL_REGISTRY_SECRET).
#
# The version baked into the binary (what the engine reports, and what every quote records) is the git sha, with a
# timestamp when the tree is dirty or has no commit yet, so two different binaries never share a version.
set -euo pipefail

cd "$(dirname "$0")/.."

version="$(git rev-parse --short=12 HEAD 2>/dev/null || echo nogit)"
if [ "$version" = nogit ] || [ -n "$(git status --porcelain -- . 2>/dev/null || true)" ]; then
  version="${version}-$(date +%Y%m%d%H%M%S)"
fi

build="$(mktemp -d)"
trap 'rm -rf "$build"' EXIT
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/version.Engine=${version}" \
  -o "${build}/solver" ./services/optimizer/cmd/solver
echo "built solver ${version} ($(wc -c < "${build}/solver" | tr -d ' ') bytes)"

if [ "${MODE:-volume}" = registry ]; then
  repo="${1:?usage: MODE=registry $0 <registry>/<repo>}"
  tag="${repo}:${version}"
  cp deploy/solver/Dockerfile "${build}/Dockerfile"
  if [ -n "${NO_PUSH:-}" ]; then
    docker buildx build --platform linux/amd64 --load -t "$tag" "$build"
  else
    docker buildx build --platform linux/amd64 --push -t "$tag" "$build"
  fi
  echo "image: ${tag}"
  echo "set MODAL_IMAGE=${tag}"
else
  go run ./services/optimizer/cmd/modalpublish -bin "${build}/solver" -version "$version"
fi
