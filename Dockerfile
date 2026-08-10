FROM golang:1.22.12-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/erlang-exporter ./cmd/erlang-exporter
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/ops-agent ./cmd/ops-agent

FROM alpine:3.20 AS exporter
RUN apk add --no-cache ca-certificates tzdata && mkdir -p /var/lib/erlang-monitor && chown 65532:65532 /var/lib/erlang-monitor
COPY --from=build /out/erlang-exporter /usr/local/bin/erlang-exporter
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/erlang-exporter"]

FROM alpine:3.20 AS ops-agent
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/ops-agent /usr/local/bin/ops-agent
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/ops-agent"]
