FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/server-status-server ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && addgroup -S app && adduser -S -G app app
ENV TZ=UTC
COPY --from=build /out/server-status-server /usr/local/bin/server-status-server
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server-status-server"]
