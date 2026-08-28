FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26.7-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY web/embed.go ./web/embed.go
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /easyagent ./cmd/easyagent

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 easyagent \
    && adduser -S -D -H -u 10001 -G easyagent easyagent \
    && mkdir -p /data \
    && chown -R easyagent:easyagent /data
WORKDIR /app
COPY --from=backend /easyagent /usr/local/bin/easyagent
USER easyagent
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -q -O - http://127.0.0.1:8080/api/v1/health >/dev/null || exit 1
ENTRYPOINT ["easyagent", "-listen", ":8080", "-db", "/data/easyagent.db"]
