QUESTION ?= Как единый агент объединяет каталоги нескольких MCP-серверов и маршрутизирует tool calls владельцу инструмента?
INDEX ?= ../day-21/artifacts/index-structural.json
TOP_K ?= 5

.PHONY: ask ask-plain ask-rag eval test

ask:
	go run ./cmd/rag-agent ask --mode both --question "$(QUESTION)" --index "$(INDEX)" --top-k $(TOP_K)

ask-plain:
	go run ./cmd/rag-agent ask --mode plain --question "$(QUESTION)"

ask-rag:
	go run ./cmd/rag-agent ask --mode rag --question "$(QUESTION)" --index "$(INDEX)" --top-k $(TOP_K)

eval:
	go run ./cmd/rag-agent eval --questions eval/questions.json --index "$(INDEX)" --top-k $(TOP_K) --out artifacts

test:
	go test ./...
	go vet ./...
