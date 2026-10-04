QUESTION ?= Как Artifact MCP безопасно сохраняет отчёты и предотвращает выход пути за разрешённый каталог?
INDEX ?= ../day-21/artifacts/index-structural.json
CANDIDATE_K ?= 20
FINAL_K ?= 5
CHUNK_MIN_SIMILARITY ?= 0.45
ANSWER_MIN_RELEVANCE ?= 0.55
PIPELINE_FLAGS = --index "$(INDEX)" --candidate-k $(CANDIDATE_K) --final-k $(FINAL_K) --chunk-min-similarity $(CHUNK_MIN_SIMILARITY) --answer-min-relevance $(ANSWER_MIN_RELEVANCE)

.PHONY: ask-day23 ask-grounded ask-strict calibrate-gate eval eval-abstention test vet

ask-day23:
	go run ./cmd/rag-agent ask --mode day23 --question "$(QUESTION)" $(PIPELINE_FLAGS)

ask-grounded:
	go run ./cmd/rag-agent ask --mode grounded --question "$(QUESTION)" $(PIPELINE_FLAGS)

ask-strict:
	go run ./cmd/rag-agent ask --mode strict --question "$(QUESTION)" $(PIPELINE_FLAGS)

calibrate-gate:
	go run ./cmd/rag-agent calibrate-gate --questions eval/questions.json --abstention-questions eval/abstention_questions.json --out artifacts $(PIPELINE_FLAGS)

eval:
	go run ./cmd/rag-agent eval --questions eval/questions.json --out artifacts $(PIPELINE_FLAGS)

eval-abstention:
	go run ./cmd/rag-agent eval-abstention --questions eval/abstention_questions.json --out artifacts $(PIPELINE_FLAGS)

test:
	go test -count=1 ./...

vet:
	go vet ./...
