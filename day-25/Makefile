.PHONY: web chat ask show-session show-memory reset-session eval-scenarios test vet

SESSION ?= artifact-audit
MODE ?= task-memory
MESSAGE ?= Наша цель — проверить безопасность Artifact MCP.

web:
	go run ./cmd/rag-web

chat:
	go run ./cmd/rag-chat chat --session "$(SESSION)" --mode "$(MODE)"

ask:
	go run ./cmd/rag-chat ask --session "$(SESSION)" --mode "$(MODE)" --message "$(MESSAGE)"

show-session:
	go run ./cmd/rag-chat show-session --session "$(SESSION)"

show-memory:
	go run ./cmd/rag-chat show-memory --session "$(SESSION)"

reset-session:
	go run ./cmd/rag-chat reset-session --session "$(SESSION)"

eval-scenarios:
	go run ./cmd/rag-chat eval-scenarios

test:
	go test -count=1 ./...

vet:
	go vet ./...
