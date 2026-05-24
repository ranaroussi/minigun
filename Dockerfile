FROM golang:1.22-alpine AS build
WORKDIR /src
COPY src/go.mod src/go.sum ./
RUN go mod download
COPY src/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/minigun .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata tini wget && \
    addgroup -S minigun && \
    adduser -S -G minigun -h /data minigun && \
    mkdir -p /data && chown -R minigun:minigun /data
COPY --from=build /out/minigun /usr/local/bin/minigun
USER minigun:minigun
WORKDIR /data
EXPOSE 8080
VOLUME ["/data"]
ENV MINIGUN_DB_PATH=/data/minigun.db \
    MINIGUN_LISTEN_ADDR=:8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- "http://127.0.0.1:8080/healthz" >/dev/null 2>&1 || exit 1
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/minigun"]
CMD ["serve"]
