# Image of the solver that runs in a Modal sandbox: one static binary on a minimal Alpine.
#
# Alpine rather than distroless: Modal's own documentation runs sandboxes on alpine, and a shell in the image
# is what Modal's sandbox tooling may expect. The binary needs nothing from it; the sandbox runs without
# network and without secrets.
#
# Build the binary first (the toolchain stays outside the image, so the image stays tiny):
#   scripts/publish-solver.sh <registry>/<repo>
FROM alpine:3.21
COPY solver /solver
ENTRYPOINT ["/solver"]
