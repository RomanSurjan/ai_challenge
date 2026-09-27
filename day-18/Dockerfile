FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/github-mcp-server ./cmd/github-mcp-server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/currency-mcp-server ./cmd/currency-mcp-server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agent-app ./cmd/agent-app

FROM alpine:3.23

RUN addgroup -S app && adduser -S -G app app && \
    mkdir -p /data && chown app:app /data
COPY --from=build /out/github-mcp-server /usr/local/bin/github-mcp-server
COPY --from=build /out/currency-mcp-server /usr/local/bin/currency-mcp-server
COPY --from=build /out/agent-app /usr/local/bin/agent-app

USER app
WORKDIR /data
EXPOSE 8080 8083 8085
ENTRYPOINT ["/usr/local/bin/agent-app"]
CMD ["-serve"]
