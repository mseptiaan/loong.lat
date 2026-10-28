# ai: build with the context at backend/ (compose.yaml next to this file does):
#   docker build -f services/ai/Dockerfile -t loong/ai .
# No secret is baked in: META_API_KEY and AI_TOKEN are read from the environment when the container starts.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# A service imports only pkg/ and its own directory (boundaries_test.go enforces it), so that is all the build needs.
COPY pkg ./pkg
COPY services/ai ./services/ai
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/ai ./services/ai/cmd/ai

FROM alpine:3.21
# CA certificates for the HTTPS this service speaks (api.meta.ai).
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
COPY --from=build /out/ai /usr/local/bin/ai
USER app
EXPOSE 8091
ENTRYPOINT ["/usr/local/bin/ai"]
