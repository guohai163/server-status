ARG VERSION=dev

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY scripts/build-release-agent.sh scripts/write-release-checksums.sh ./scripts/
ARG VERSION
RUN version="${VERSION#v}"; mkdir -p /out/agent-release; \
    if printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$'; then \
      ./scripts/build-release-agent.sh "$version" /out/agent-release; \
    fi
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X github.com/guohai/server-status/internal/server.Version=$VERSION" \
    -o /out/server-status-server ./cmd/server

FROM --platform=$BUILDPLATFORM golang:1.20.14-alpine AS build-windows-agent
WORKDIR /src
COPY windows-agent ./windows-agent
RUN apk add --no-cache ca-certificates curl
COPY scripts/build-release-windows-agent.sh scripts/fetch-smartmontools-windows.sh scripts/write-release-checksums.sh ./scripts/
ARG VERSION
RUN version="${VERSION#v}"; mkdir -p /out/agent-release; \
    if printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$'; then \
      ./scripts/build-release-windows-agent.sh "$version" /out/agent-release; \
    fi

FROM alpine:3.22 AS build-macos-agent
WORKDIR /src
COPY macos-agent ./macos-agent
COPY scripts/build-release-macos-agent.sh scripts/write-release-checksums.sh ./scripts/
ARG VERSION
RUN version="${VERSION#v}"; mkdir -p /out/agent-release; \
    if printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$'; then \
      ./scripts/build-release-macos-agent.sh "$version" /out/agent-release; \
    fi

FROM alpine:3.22 AS bundle-agent
ARG VERSION
COPY scripts/write-release-checksums.sh /usr/local/bin/write-release-checksums
COPY --from=build /out/agent-release /tmp/linux
COPY --from=build-windows-agent /out/agent-release /tmp/windows
COPY --from=build-macos-agent /out/agent-release /tmp/macos
RUN version="${VERSION#v}"; mkdir -p /out; \
    if printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$'; then \
      release="/out/v$version"; mkdir -p "$release"; \
      cp /tmp/linux/server-status-agent-* "$release/"; \
      cp /tmp/windows/server-status-* "$release/"; \
      cp /tmp/macos/server-status-agent-* "$release/"; \
      /usr/local/bin/write-release-checksums "$release"; \
      ln -s "v$version" /out/latest; \
    fi

FROM alpine:3.22
LABEL org.opencontainers.image.source="https://github.com/guohai163/server-status"
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app \
    && adduser -S -G app app \
    && mkdir -p /var/cache/server-status \
    && chown app:app /var/cache/server-status
ENV TZ=UTC \
    SERVER_STATUS_RELEASE_CACHE_DIR=/var/cache/server-status
COPY --from=build /out/server-status-server /usr/local/bin/server-status-server
COPY --from=bundle-agent /out/ /usr/local/share/server-status/agent-releases/
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server-status-server"]
