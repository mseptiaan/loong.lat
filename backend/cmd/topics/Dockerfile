# topics: creates the Kafka topics (and their dead-letter topics) once before the services start; safe to rerun.
# Build with the context at backend/:
#   docker build -f cmd/topics/Dockerfile -t loong/topics .
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY pkg ./pkg
COPY cmd/topics ./cmd/topics
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/topics ./cmd/topics

FROM alpine:3.21
RUN adduser -D -u 10001 app
COPY --from=build /out/topics /usr/local/bin/topics
USER app
ENTRYPOINT ["/usr/local/bin/topics"]
