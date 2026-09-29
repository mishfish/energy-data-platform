# Shared image for the Go services in cmd/. Build one with: docker build --build-arg SERVICE=ingest .
ARG GO_VERSION=1.27

FROM golang:${GO_VERSION} AS build
ARG SERVICE
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/service ./cmd/${SERVICE}

FROM alpine:3.24
RUN adduser -D -u 10001 app
COPY --from=build /out/service /usr/local/bin/service
USER app
ENTRYPOINT ["/usr/local/bin/service"]
