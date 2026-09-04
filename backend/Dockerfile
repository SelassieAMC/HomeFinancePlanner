# syntax=docker/dockerfile:1

# --- Build stage -------------------------------------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
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
