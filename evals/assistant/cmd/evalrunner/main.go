package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	evals "github.com/nexusmart/core/evals/assistant"
)

func main() {
	defaultMode := strings.TrimSpace(os.Getenv("LLM_MODE"))
	if defaultMode == "" {
		defaultMode = "mock"
	}

	mode := flag.String("mode", defaultMode, "LLM provider mode: mock or anthropic")
	datasetPath := flag.String("dataset", "dataset.json", "Path to 100-case Vietnamese evaluation dataset JSON")
	injectionPath := flag.String("injection", "prompt_injection.json", "Path to prompt injection dataset JSON")
	outPath := flag.String("out", "", "Optional output path for JSON evaluation report")
	limit := flag.Int("limit", 0, "Optional limit on number of cases per suite (0 = run all)")
	flag.Parse()

	cfg := evals.RunConfig{
		Mode:             *mode,
		Model:            strings.TrimSpace(os.Getenv("LLM_MODEL")),
		AnthropicAPIKey:  strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")),
		AnthropicBaseURL: strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL")),
		DatasetPath:      *datasetPath,
		InjectionPath:    *injectionPath,
		Limit:            *limit,
	}

	report, err := evals.Run(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "evalrunner error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(evals.FormatMarkdownSummary(report))

	if strings.TrimSpace(*outPath) != "" {
		if dir := filepath.Dir(*outPath); dir != "" && dir != "." {
			if mkErr := os.MkdirAll(dir, 0o750); mkErr != nil {
				fmt.Fprintf(os.Stderr, "create output directory: %v\n", mkErr)
				os.Exit(1)
			}
		}
		encoded, mErr := json.MarshalIndent(report, "", "  ")
		if mErr != nil {
			fmt.Fprintf(os.Stderr, "marshal json report: %v\n", mErr)
			os.Exit(1)
		}
		if wErr := os.WriteFile(*outPath, encoded, 0o600); wErr != nil {
			fmt.Fprintf(os.Stderr, "write json report: %v\n", wErr)
			os.Exit(1)
		}
		fmt.Printf("\nWrote JSON report to %s\n", *outPath)
	}

	if len(report.GateFailures) > 0 {
		os.Exit(1)
	}
}
