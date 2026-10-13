# syntax=docker/dockerfile:1

# --- Build stage -------------------------------------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# -tags nodynamic: gen2brain/heic's default "dynamic" decoder imports
# purego, which makes even a CGO_ENABLED=0 binary carry a musl interpreter
# (exec then fails in distroless with "no such file or directory" — and
# silently, since docker compose up only surfaces it via the healthcheck).
# The tag keeps the WASM decoder path, which is what this server actually
# wants.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -tags nodynamic -ldflags="-s -w" -o /out/server ./cmd/server \
    && mkdir /data-dir

# --- Runtime stage -----------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/server /app/server
# /data is owned by the nonroot user (uid 65532) so SQLite can create the
# database and its WAL files; the named volume inherits this on first use.
COPY --from=build --chown=65532:65532 /data-dir /data
ENV PORT=8080 DB_PATH=/data/finance.db APP_ENV=production
EXPOSE 8080
VOLUME ["/data"]
ENTRYPOINT ["/app/server"]
