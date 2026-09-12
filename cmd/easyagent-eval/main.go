// Command easyagent-eval compares model/provider configurations with the same
// native Function Calling qualification enforced by the EasyAgent server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
	"github.com/lakernote/easy-agent/internal/agent/modeladapter"
	"github.com/lakernote/easy-agent/internal/agent/qualification"
)

type modelSummary struct {
	Model                  string   `json:"model"`
	Runs                   int      `json:"runs"`
	Passed                 int      `json:"passed"`
	PassRate               float64  `json:"passRate"`
	AverageInputTokens     int      `json:"averageInputTokens"`
	AverageOutputTokens    int      `json:"averageOutputTokens"`
	AverageTotalTokens     int      `json:"averageTotalTokens"`
	AverageDurationMS      int64    `json:"averageDurationMs"`
	AverageFirstTokenMS    int64    `json:"averageFirstTokenMs"`
	Errors                 []string `json:"errors,omitempty"`
	totalInputTokens       int
	totalOutputTokens      int
	totalDuration          time.Duration
	totalFirstToken        time.Duration
	firstTokenObservations int
}

func main() {
	os.Exit(run())
}

func run() int {
	provider := flag.String("provider", "ollama", "provider label")
	protocol := flag.String("protocol", modeladapter.ProtocolOllamaChat, "model protocol")
	baseURL := flag.String("base-url", "http://127.0.0.1:11434", "provider base URL")
	modelsFlag := flag.String("models", "", "comma-separated model names")
	runs := flag.Int("runs", 1, "qualification runs per model")
	timeout := flag.Duration("timeout", 5*time.Minute, "timeout for one provider request")
	contextWindow := flag.Int("context-window", 0, "optional model context window")
	apiKeyEnv := flag.String("api-key-env", "", "environment variable containing the API key")
	output := flag.String("output", "table", "table or json")
	failOnError := flag.Bool("fail-on-error", false, "exit non-zero when any run fails")
	flag.Parse()

	models := splitModels(*modelsFlag)
	if len(models) == 0 || *runs <= 0 || *timeout <= 0 || *contextWindow < 0 {
		fmt.Fprintln(os.Stderr, "-models, positive -runs/-timeout and non-negative -context-window are required")
		return 2
	}
	apiKey := ""
	if name := strings.TrimSpace(*apiKeyEnv); name != "" {
		apiKey = os.Getenv(name)
		if apiKey == "" {
			fmt.Fprintf(os.Stderr, "environment variable %s is empty\n", name)
			return 2
		}
	}

	summaries := make([]modelSummary, 0, len(models))
	failed := false
	for _, modelName := range models {
		summary := modelSummary{Model: modelName, Runs: *runs}
		for runIndex := 0; runIndex < *runs; runIndex++ {
			model, err := modeladapter.New(modeladapter.Config{
				Provider: *provider, Protocol: *protocol, BaseURL: *baseURL, APIKey: apiKey,
				Timeout: *timeout, DisableThinking: true, ContextWindowTokens: *contextWindow,
			})
			if err == nil {
				capabilities := agent.CapabilitiesOf(model)
				if !capabilities.NativeTools || !capabilities.Streaming {
					err = fmt.Errorf("协议 %s 必须同时支持原生 Function Calling 和流式 API", *protocol)
				}
			}
			result := qualification.Result{Model: modelName}
			if err == nil {
				requestContext, cancel := context.WithTimeout(context.Background(), *timeout*2)
				result, err = qualification.Run(requestContext, model, modelName)
				cancel()
			}
			summary.totalInputTokens += result.InputTokens
			summary.totalOutputTokens += result.OutputTokens
			summary.totalDuration += result.Duration
			if result.FirstTokenDuration > 0 {
				summary.totalFirstToken += result.FirstTokenDuration
				summary.firstTokenObservations++
			}
			if err == nil {
				summary.Passed++
			} else {
				failed = true
				summary.Errors = appendUnique(summary.Errors, err.Error())
			}
		}
		summary.finish()
		summaries = append(summaries, summary)
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		if summaries[i].PassRate != summaries[j].PassRate {
			return summaries[i].PassRate > summaries[j].PassRate
		}
		if summaries[i].AverageDurationMS != summaries[j].AverageDurationMS {
			return summaries[i].AverageDurationMS < summaries[j].AverageDurationMS
		}
		return summaries[i].Model < summaries[j].Model
	})
	if *output == "json" {
		encoded, _ := json.MarshalIndent(summaries, "", "  ")
		fmt.Println(string(encoded))
	} else if *output == "table" {
		printTable(summaries)
	} else {
		fmt.Fprintln(os.Stderr, "-output must be table or json")
		return 2
	}
	if failed && *failOnError {
		return 1
	}
	return 0
}

func (summary *modelSummary) finish() {
	if summary.Runs == 0 {
		return
	}
	summary.PassRate = float64(summary.Passed) / float64(summary.Runs)
	summary.AverageInputTokens = summary.totalInputTokens / summary.Runs
	summary.AverageOutputTokens = summary.totalOutputTokens / summary.Runs
	summary.AverageTotalTokens = summary.AverageInputTokens + summary.AverageOutputTokens
	summary.AverageDurationMS = summary.totalDuration.Milliseconds() / int64(summary.Runs)
	if summary.firstTokenObservations > 0 {
		summary.AverageFirstTokenMS = summary.totalFirstToken.Milliseconds() / int64(summary.firstTokenObservations)
	}
}

func splitModels(value string) []string {
	seen := map[string]struct{}{}
	result := []string{}
	for _, candidate := range strings.Split(value, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		result = append(result, candidate)
	}
	return result
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func printTable(summaries []modelSummary) {
	writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "MODEL\tPASS\tAVG TOKENS\tAVG TIME\tFIRST OUTPUT\tERROR")
	for _, summary := range summaries {
		errText := ""
		if len(summary.Errors) > 0 {
			errText = summary.Errors[0]
			if len([]rune(errText)) > 96 {
				errText = string([]rune(errText)[:96]) + "…"
			}
		}
		fmt.Fprintf(writer, "%s\t%d/%d\t%d\t%s\t%s\t%s\n", summary.Model, summary.Passed, summary.Runs,
			summary.AverageTotalTokens, time.Duration(summary.AverageDurationMS)*time.Millisecond,
			time.Duration(summary.AverageFirstTokenMS)*time.Millisecond, errText)
	}
	_ = writer.Flush()
}
