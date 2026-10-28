# console-api: build with the context at backend/ (compose.yaml next to this file does):
#   docker build -f services/console/Dockerfile -t loong/console-api .
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# A service imports only pkg/ and its own directory (boundaries_test.go enforces it), so that is all the build needs.
COPY pkg ./pkg
COPY services/console ./services/console
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w" \
      -o /out/console-api ./services/console/cmd/console-api

FROM alpine:3.21
# CA certificates for the HTTPS this service speaks (Mapbox, Modal, Kafka over TLS in production).
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
COPY --from=build /out/console-api /usr/local/bin/console-api
USER app
EXPOSE 8083
ENTRYPOINT ["/usr/local/bin/console-api"]
