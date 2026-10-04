package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"ai-challenge/day-25/internal/app"
	"ai-challenge/day-25/internal/chatservice"
	"ai-challenge/day-25/internal/chatstore"
	"ai-challenge/day-25/internal/evaluation"
	"ai-challenge/day-25/internal/evidence"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("a command is required")
	}
	switch args[0] {
	case "chat":
		return runChat(ctx, args[1:])
	case "ask":
		return runAsk(ctx, args[1:])
	case "show-session":
		return runShowSession(ctx, args[1:])
	case "show-memory":
		return runShowMemory(ctx, args[1:])
	case "list-sessions":
		return runListSessions(ctx, args[1:])
	case "reset-session":
		return runResetSession(ctx, args[1:])
	case "eval-scenarios":
		return runEvalScenarios(ctx, args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: rag-chat <chat|ask|show-session|show-memory|list-sessions|reset-session|eval-scenarios> [flags]")
}

func addCommonFlags(f *flag.FlagSet) *app.Config {
	config := app.ConfigFromEnv()
	config.BindFlags(f)
	return &config
}

func runAsk(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("ask", flag.ContinueOnError)
	sessionID := f.String("session", "default", "session ID")
	modeValue := f.String("mode", string(chatservice.TaskMemory), "stateless, history, or task-memory")
	message := f.String("message", "", "current user message")
	jsonOutput := f.Bool("json", false, "print complete turn JSON")
	common := addCommonFlags(f)
	if err := f.Parse(args); err != nil {
		return err
	}
	mode, err := app.ParseMode(*modeValue)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*message) == "" {
		return fmt.Errorf("--message is required")
	}
	rt, err := common.Build()
	if err != nil {
		return err
	}
	result, err := rt.Service.Ask(ctx, *sessionID, mode, *message)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(result)
	}
	printTurn(result)
	return nil
}

func runChat(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("chat", flag.ContinueOnError)
	sessionID := f.String("session", "default", "session ID")
	modeValue := f.String("mode", string(chatservice.TaskMemory), "stateless, history, or task-memory")
	common := addCommonFlags(f)
	if err := f.Parse(args); err != nil {
		return err
	}
	mode, err := app.ParseMode(*modeValue)
	if err != nil {
		return err
	}
	rt, err := common.Build()
	if err != nil {
		return err
	}
	fmt.Printf("Day 25 RAG chat — session %s, mode %s. /help for commands.\n", *sessionID, mode)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch line {
		case "/exit":
			return nil
		case "/help":
			fmt.Println("/memory /history /sources /state /new /help /exit")
			continue
		case "/new":
			if err := rt.Store.Reset(ctx, *sessionID); err != nil && !errors.Is(err, chatstore.ErrNotFound) {
				return err
			}
			fmt.Println("Новая пустая session будет создана следующим сообщением.")
			continue
		case "/memory", "/state":
			session, err := rt.Store.Load(ctx, *sessionID)
			if err != nil {
				return err
			}
			if err := printJSON(session.TaskState); err != nil {
				return err
			}
			continue
		case "/history":
			session, err := rt.Store.Load(ctx, *sessionID)
			if err != nil {
				return err
			}
			if err := printJSON(session.Messages); err != nil {
				return err
			}
			continue
		case "/sources":
			session, err := rt.Store.Load(ctx, *sessionID)
			if err != nil {
				return err
			}
			if len(session.Turns) == 0 {
				fmt.Println("Источников пока нет.")
				continue
			}
			var rag struct {
				Sources   []evidence.Source   `json:"sources"`
				Citations []evidence.Citation `json:"citations"`
			}
			_ = json.Unmarshal(session.Turns[len(session.Turns)-1].RAGResult, &rag)
			if err := printJSON(rag); err != nil {
				return err
			}
			continue
		}
		result, err := rt.Service.Ask(ctx, *sessionID, mode, line)
		if err != nil {
			fmt.Fprintln(os.Stderr, "turn error:", err)
			continue
		}
		printTurn(result)
	}
	return scanner.Err()
}

func runShowSession(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("show-session", flag.ContinueOnError)
	id := f.String("session", "", "session ID")
	config := app.ConfigFromEnv()
	dir := f.String("store-dir", config.StoreDir, "session directory")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--session is required")
	}
	store, err := chatstore.New(*dir)
	if err != nil {
		return err
	}
	session, err := store.Load(ctx, *id)
	if err != nil {
		return err
	}
	return printJSON(session)
}

func runShowMemory(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("show-memory", flag.ContinueOnError)
	id := f.String("session", "", "session ID")
	config := app.ConfigFromEnv()
	dir := f.String("store-dir", config.StoreDir, "session directory")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--session is required")
	}
	store, err := chatstore.New(*dir)
	if err != nil {
		return err
	}
	session, err := store.Load(ctx, *id)
	if err != nil {
		return err
	}
	return printJSON(session.TaskState)
}

func runListSessions(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("list-sessions", flag.ContinueOnError)
	config := app.ConfigFromEnv()
	dir := f.String("store-dir", config.StoreDir, "session directory")
	if err := f.Parse(args); err != nil {
		return err
	}
	store, err := chatstore.New(*dir)
	if err != nil {
		return err
	}
	items, err := store.List(ctx)
	if err != nil {
		return err
	}
	return printJSON(items)
}

func runResetSession(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("reset-session", flag.ContinueOnError)
	id := f.String("session", "", "exact session ID to remove")
	config := app.ConfigFromEnv()
	dir := f.String("store-dir", config.StoreDir, "session directory")
	if err := f.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--session is required")
	}
	store, err := chatstore.New(*dir)
	if err != nil {
		return err
	}
	if err := store.Reset(ctx, *id); err != nil {
		return err
	}
	fmt.Printf("session %s removed\n", *id)
	return nil
}

func runEvalScenarios(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("eval-scenarios", flag.ContinueOnError)
	scenarioPath := f.String("scenarios", "eval/scenarios.json", "fixed scenario dataset")
	out := f.String("out", "artifacts", "artifact directory")
	jsonOutput := f.Bool("json", false, "print summary JSON")
	common := addCommonFlags(f)
	if err := f.Parse(args); err != nil {
		return err
	}
	scenarios, sha, err := evaluation.LoadScenarios(*scenarioPath)
	if err != nil {
		return err
	}
	rt, err := common.Build()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "evaluating %d fixed scenarios in stateless, history, task-memory (SHA %s)...\n", len(scenarios), sha)
	report := evaluation.RunScenarios(ctx, rt.Service, scenarios, evaluation.ScenarioReport{Dataset: *scenarioPath, ScenarioSHA256: sha, Models: map[string]string{"embedding": common.EmbedModel, "memory": common.MemoryModel, "resolver": common.ResolverModel, "answer": common.ChatModel, "judge": common.JudgeModel}, Index: common.IndexPath, IndexCorpusID: rt.Index.CorpusID, Pipeline: rt.RAG.Pipeline, QuoteBounds: fmt.Sprintf("%d–%d Unicode characters", common.QuoteMin, common.QuoteMax), Limits: rt.Service.Limits})
	if err := evaluation.SaveScenarioArtifacts(*out, report); err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(report.Summary)
	}
	for _, mode := range []chatservice.Mode{chatservice.Stateless, chatservice.History, chatservice.TaskMemory} {
		m := report.Summary[mode]
		fmt.Printf("%s: goal %.3f, follow-up %.3f, answered/abstained/memory %d/%d/%d, exact quotes %.3f, avg %.1f ms\n", mode, m.GoalRetentionRate, m.FollowUpResolutionSuccess, m.Answered, m.Abstained, m.MemoryOnly, m.ExactSubstringQuoteRate, m.AverageTotalMS)
	}
	fmt.Printf("artifacts: %s/scenario-evaluation.json and Markdown reports\n", *out)
	return nil
}

func printTurn(result chatservice.TurnResult) {
	fmt.Printf("[%s] %s\n", result.Record.Status, result.Record.Answer)
	if result.Record.ClarificationQuestion != "" {
		fmt.Println("Уточнение:", result.Record.ClarificationQuestion)
	}
	for _, source := range result.RAG.Sources {
		fmt.Printf("  %s (%s) %s — %s\n", source.ID, source.Kind, source.Source, source.Section)
	}
	for _, citation := range result.RAG.Citations {
		fmt.Printf("    exact quote %s: %q\n", citation.SourceID, citation.Quote)
	}
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
