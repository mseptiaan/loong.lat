# driver-api: build with the context at backend/ (compose.yaml next to this file does):
#   docker build -f services/driver/Dockerfile -t loong/driver-api .
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# A service imports only pkg/ and its own directory (boundaries_test.go enforces it), so that is all the build needs.
COPY pkg ./pkg
COPY services/driver ./services/driver
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w" \
      -o /out/driver-api ./services/driver/cmd/driver-api

FROM alpine:3.21
# CA certificates for the HTTPS this service speaks (Mapbox, Modal, Kafka over TLS in production).
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
COPY --from=build /out/driver-api /usr/local/bin/driver-api
USER app
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/driver-api"]
