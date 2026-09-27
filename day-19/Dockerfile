FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mcp-server ./cmd/mcp-server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agent-app ./cmd/agent-app

FROM alpine:3.23
RUN apk add --no-cache ca-certificates wget && \
    addgroup -S app && adduser -S -G app app && \
    mkdir -p /data/outputs && chown -R app:app /data
COPY --from=build /out/mcp-server /usr/local/bin/mcp-server
COPY --from=build /out/agent-app /usr/local/bin/agent-app
USER app
WORKDIR /data
EXPOSE 8086 8087
