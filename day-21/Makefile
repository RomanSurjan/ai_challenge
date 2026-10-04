INPUTS ?= ../day-16,../day-17,../day-18,../day-19,../day-20
QUERY ?= Как агент маршрутизирует вызовы MCP?

.PHONY: corpus index compare search test

corpus:
	go run ./cmd/rag corpus --input "$(INPUTS)" --out artifacts/corpus-manifest.json

index:
	go run ./cmd/rag index --input "$(INPUTS)" --strategy all --out artifacts

compare:
	go run ./cmd/rag compare --fixed artifacts/index-fixed.json --structural artifacts/index-structural.json --eval eval/questions.json --out artifacts

search:
	go run ./cmd/rag search --index artifacts/index-structural.json --query "$(QUERY)" --top-k 5

test:
	go test ./...
	go vet ./...
