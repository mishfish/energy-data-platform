# Shared image for the Go services in cmd/. Build one with: docker build --build-arg SERVICE=ingest .
# Services that need cgo (sink: confluent-kafka-go, ADR 0028) set --build-arg CGO=1. The builder is
# Alpine, so a cgo binary links against musl and runs on the Alpine runtime image; librdkafka is
# bundled statically by confluent-kafka-go (build tag musl).
ARG GO_VERSION=1.27

FROM golang:${GO_VERSION}-alpine AS build
ARG SERVICE
ARG CGO=0
RUN if [ "$CGO" = "1" ]; then apk add --no-cache gcc musl-dev; fi
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=${CGO} go build -tags musl -trimpath -ldflags="-s -w" -o /out/service ./cmd/${SERVICE}

FROM alpine:3.24
RUN adduser -D -u 10001 app
COPY --from=build /out/service /usr/local/bin/service
USER app
ENTRYPOINT ["/usr/local/bin/service"]
