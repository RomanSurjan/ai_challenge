FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
ENV GOFLAGS=-mod=vendor
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /out/knowledge-mcp ./cmd/knowledge-mcp && CGO_ENABLED=0 go build -trimpath -o /out/github-mcp ./cmd/github-mcp && CGO_ENABLED=0 go build -trimpath -o /out/artifact-mcp ./cmd/artifact-mcp && CGO_ENABLED=0 go build -trimpath -o /out/agent-app ./cmd/agent-app
FROM alpine:3.23
RUN apk add --no-cache ca-certificates wget && addgroup -S app && adduser -S -G app app && mkdir -p /data/outputs && chown -R app:app /data
COPY --from=build /out/* /usr/local/bin/
USER app
WORKDIR /data
EXPOSE 8088 8089 8090 8091
