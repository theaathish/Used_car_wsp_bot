# Single-service Railway build. No Node stage: admin is prebuilt static in web/dist.
FROM golang:1.27-bookworm AS build
RUN apt-get update && apt-get install -y gcc libc6-dev && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="-s -w" -o /server ./cmd/server

FROM debian:bookworm-slim
# ca-certificates (WA TLS) + postgresql-client (pg_dump/psql for the
# backup cron + restore drill — the #1 production data-safety tool).
RUN apt-get update && apt-get install -y ca-certificates postgresql-client && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /server /app/server
ENV PORT=8080 DATA_DIR=/data GOMAXPROCS=1 GOGC=20
EXPOSE 8080
CMD ["/app/server"]
