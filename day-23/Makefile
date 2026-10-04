QUESTION ?= Как Artifact MCP безопасно сохраняет отчёты и предотвращает выход пути за разрешённый каталог?
INDEX ?= ../day-21/artifacts/index-structural.json
CANDIDATE_K ?= 20
FINAL_K ?= 5
MIN_SIMILARITY ?= 0.45
PIPELINE_FLAGS = --index "$(INDEX)" --candidate-k $(CANDIDATE_K) --final-k $(FINAL_K) --min-similarity $(MIN_SIMILARITY)

.PHONY: ask-baseline ask-filtered ask-enhanced sweep eval test vet

ask-baseline:
	go run ./cmd/rag-agent ask --mode baseline --question "$(QUESTION)" $(PIPELINE_FLAGS)

ask-filtered:
	go run ./cmd/rag-agent ask --mode filtered --question "$(QUESTION)" $(PIPELINE_FLAGS)

ask-enhanced:
	go run ./cmd/rag-agent ask --mode enhanced --question "$(QUESTION)" $(PIPELINE_FLAGS)

sweep:
	go run ./cmd/rag-agent sweep --questions eval/questions.json --out artifacts $(PIPELINE_FLAGS)

eval:
	go run ./cmd/rag-agent eval --questions eval/questions.json --out artifacts $(PIPELINE_FLAGS)

test:
	go test ./...

vet:
	go vet ./...
