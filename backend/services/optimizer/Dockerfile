# optimizer: build with the context at backend/ (compose.yaml next to this file does):
#   docker build -f services/optimizer/Dockerfile -t loong/optimizer .
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# A service imports only pkg/ and its own directory (boundaries_test.go enforces it), so that is all the build needs.
COPY pkg ./pkg
COPY services/optimizer ./services/optimizer
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/mseptiaan/loong.lat/backend/services/optimizer/internal/version.Engine=${VERSION}" \
      -o /out/optimizer ./services/optimizer/cmd/optimizer

FROM alpine:3.21
# CA certificates for the HTTPS this service speaks (Mapbox, Modal, Kafka over TLS in production).
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
COPY --from=build /out/optimizer /usr/local/bin/optimizer
USER app
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/optimizer"]
