FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X github.com/guohai/server-status/internal/server.Version=$VERSION" \
    -o /out/server-status-server ./cmd/server

FROM alpine:3.22
LABEL org.opencontainers.image.source="https://github.com/guohai163/server-status"
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S -G app app \
    && mkdir -p /var/cache/server-status \
    && chown app:app /var/cache/server-status
ENV TZ=UTC \
    SERVER_STATUS_RELEASE_CACHE_DIR=/tmp/server-status-release-cache
COPY --from=build /out/server-status-server /usr/local/bin/server-status-server
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server-status-server"]
