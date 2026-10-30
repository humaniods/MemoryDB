# One static binary is the whole database, so this image needs no runtime dependencies.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ui.html console.html ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /memorydb .

FROM alpine:3.20
RUN addgroup -g 10001 app && adduser -D -u 10001 -G app app && \
    mkdir -p /data && chown app:app /data
COPY --from=build /memorydb /usr/local/bin/memorydb
USER app
EXPOSE 8080
VOLUME /data
# Every endpoint, health included, answers 401 without a bearer token, so the check carries one.
# The token is the container's own environment value, never baked into the image.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD wget -q -O /dev/null --header="Authorization: Bearer $MEMORYDB_TOKEN" http://127.0.0.1:8080/api/v1/health/ || exit 1
CMD ["memorydb", "-addr", "0.0.0.0:8080", "-data", "/data/memory.wal", "-worker", "-allow-insecure-network"]
