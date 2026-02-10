package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"reactsql/internal/adapter"
	"reactsql/internal/inference"
	"reactsql/internal/llm"

	"github.com/tmc/langchaingo/llms"
)

// ─────────────────────────────────────────────────────
// Data structures
// ─────────────────────────────────────────────────────

// SpiderExample Spider dataset example
type SpiderExample struct {
	DbID                    string   `json:"db_id"`
	Query                   string   `json:"query"`
	Question                string   `json:"question"`
	ResultFields            []string `json:"result_fields"`
	ResultFieldsDescription string   `json:"result_fields_description"`
}

// BirdExample BIRD dataset example
type BirdExample struct {
	QuestionID int    `json:"question_id"`
	DbID       string `json:"db_id"`
	Question   string `json:"question"`
	Evidence   string `json:"evidence"`
	SQL        string `json:"SQL"`
	Difficulty string `json:"difficulty"`
}

// EvalResult unified evaluation result
type EvalResult struct {
	QuestionID     int                   `json:"question_id,omitempty"`
	DbID           string                `json:"db_id"`
	Question       string                `json:"question"`
	Evidence       string                `json:"evidence,omitempty"`
	GoldSQL        string                `json:"gold_sql"`
	GeneratedSQL   string                `json:"generated_sql"`
	Status         string                `json:"status"` // success, error, timeout
	Error          string                `json:"error,omitempty"`
	TimeSeconds    float64               `json:"time_seconds"`
	LLMCalls       int                   `json:"llm_calls"`
	TotalTokens    int                   `json:"total_tokens"`
	ClarifyCount   int                   `json:"clarify_count"`
	SelectedTables []string              `json:"selected_tables"`
	Difficulty     string                `json:"difficulty,omitempty"`
	ReActSteps     []inference.ReActStep `json:"react_steps,omitempty"`
}

// EvalMode predefined evaluation mode
type EvalMode struct {
	Name            string
	Description     string
	UseReact        bool
	UseRichContext  bool
	ReactLinking    bool
	EnableClarify   string
	EnableProofread bool
}

// ─────────────────────────────────────────────────────
// Default paths
// ─────────────────────────────────────────────────────

var defaultPaths = map[string]map[string]string{
	"spider": {
		"dev":     "benchmarks/spider_corrected/dev_with_field_with_id.json",
		"db-dir":  "benchmarks/spider/database",
		"context": "contexts/sqlite/spider",
	},
	"bird": {
		"dev":     "benchmarks/bird/dev/dev.json",
		"db-dir":  "benchmarks/bird/dev/dev_databases",
		"context": "contexts/sqlite/bird",
	},
}

// ─────────────────────────────────────────────────────
// Available modes
// ─────────────────────────────────────────────────────

var evalModes = []EvalMode{
	{
		Name:        "baseline",
		Description: "Baseline — Direct SQL generation (no ReAct, no Rich Context)",
		UseReact:    false, UseRichContext: false, ReactLinking: false,
		EnableClarify: "off", EnableProofread: false,
	},
	{
		Name:        "react",
		Description: "ReAct — Multi-step reasoning with tool use (execute_sql for validation)",
		UseReact:    true, UseRichContext: false, ReactLinking: false,
		EnableClarify: "off", EnableProofread: false,
	},
	{
		Name:        "rich_context",
		Description: "Rich Context — Enhanced schema context (pre-generated table/column descriptions)",
		UseReact:    false, UseRichContext: true, ReactLinking: false,
		EnableClarify: "off", EnableProofread: false,
	},
	{
		Name:        "react+rich_context",
		Description: "ReAct + Rich Context — ReAct reasoning with enhanced schema context",
		UseReact:    true, UseRichContext: true, ReactLinking: false,
		EnableClarify: "off", EnableProofread: false,
	},
	{
		Name:        "react+rich_context+linking",
		Description: "ReAct + Rich Context + Schema Linking — Full pipeline with ReAct-based schema linking",
		UseReact:    true, UseRichContext: true, ReactLinking: true,
		EnableClarify: "off", EnableProofread: false,
	},
	{
		Name:        "full",
		Description: "Full Pipeline — All features enabled (ReAct + Rich Context + Linking + Clarify + Proofread)",
		UseReact:    true, UseRichContext: true, ReactLinking: true,
		EnableClarify: "force", EnableProofread: true,
	},
}

func main() {
	// Command line flags
	benchmark := flag.String("benchmark", "", "Benchmark: spider | bird (if empty, will ask interactively)")
	modelType := flag.String("model", "deepseek-v3", "Model: deepseek-v3 | deepseek-v3.2 | qwen-max | qwen3-max | ali-deepseek-v3.2")
	mode := flag.String("mode", "", "Evaluation mode (if empty, will show interactive menu)")
	limit := flag.Int("limit", 0, "Limit number of examples (0 = all)")
	startIdx := flag.Int("start", 0, "Start index")
	endIdx := flag.Int("end", -1, "End index (-1 = all)")
	outputDir := flag.String("output-dir", "", "Output directory (auto-generated if empty)")
	logMode := flag.String("log-mode", "simple", "Log mode: simple | full")
	difficulty := flag.String("difficulty", "", "BIRD only: filter by difficulty (simple/moderate/challenging)")

	flag.Parse()

	reader := bufio.NewReader(os.Stdin)

	// ── Step 1: Select benchmark ──
	if *benchmark == "" {
		fmt.Println()
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("📦 Select Benchmark")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("  1. spider  — Spider dev set (1034 examples, cross-database)")
		fmt.Println("  2. bird    — BIRD dev set (1534 examples, with evidence hints)")
		fmt.Println()
		fmt.Print("Enter choice [1/2]: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		switch input {
		case "1", "spider":
			*benchmark = "spider"
		case "2", "bird":
			*benchmark = "bird"
		default:
			log.Fatalf("Invalid choice: %s", input)
		}
	}

	if *benchmark != "spider" && *benchmark != "bird" {
		log.Fatalf("Unknown benchmark: %s. Use 'spider' or 'bird'.", *benchmark)
	}

	// ── Step 2: Validate paths ──
	paths := defaultPaths[*benchmark]
	devPath := paths["dev"]
	dbDir := paths["db-dir"]
	contextDir := paths["context"]

	// Check dev file
	if _, err := os.Stat(devPath); os.IsNotExist(err) {
		log.Fatalf("❌ Dev file not found: %s\n   Please download the %s benchmark first.\n   See README.md for instructions.", devPath, *benchmark)
	}

	// Check database directory
	if _, err := os.Stat(dbDir); os.IsNotExist(err) {
		log.Fatalf("❌ Database directory not found: %s\n   Please download the %s benchmark databases first.\n   See README.md for instructions.", dbDir, *benchmark)
	}

	// Check context directory (warn, don't fail — some modes don't need it)
	contextAvailable := true
	if _, err := os.Stat(contextDir); os.IsNotExist(err) {
		contextAvailable = false
	}

	// ── Step 3: Select mode ──
	var selectedMode EvalMode
	if *mode == "" {
		fmt.Println()
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("🎯 Select Evaluation Mode")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		for i, m := range evalModes {
			available := "✅"
			note := ""
			if m.UseRichContext && !contextAvailable {
				available = "⚠️"
				note = " (Rich Context not generated yet, run gen_all_dev first)"
			}
			fmt.Printf("  %s %d. %-36s %s%s\n", available, i+1, m.Name, m.Description, note)
		}
		fmt.Println()
		fmt.Printf("Enter choice [1-%d]: ", len(evalModes))

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		// Try match by number
		idx := -1
		if _, err := fmt.Sscanf(input, "%d", &idx); err == nil && idx >= 1 && idx <= len(evalModes) {
			selectedMode = evalModes[idx-1]
		} else {
			// Try match by name
			for _, m := range evalModes {
				if m.Name == input {
					selectedMode = m
					break
				}
			}
			if selectedMode.Name == "" {
				log.Fatalf("Invalid choice: %s", input)
			}
		}
	} else {
		// Match by name from --mode flag
		for _, m := range evalModes {
			if m.Name == *mode {
				selectedMode = m
				break
			}
		}
		if selectedMode.Name == "" {
			log.Fatalf("Unknown mode: %s. Available: baseline, react, rich_context, react+rich_context, react+rich_context+linking, full", *mode)
		}
	}

	// Validate Rich Context availability
	if selectedMode.UseRichContext && !contextAvailable {
		log.Fatalf("❌ Rich Context directory not found: %s\n   This mode requires Rich Context. Generate it first:\n   go run ./cmd/gen_all_dev --benchmark %s", contextDir, *benchmark)
	}

	// ── Step 4: Parse model ──
	modelTypeEnum := parseModelType(*modelType)
	modelDisplayName := llm.GetModelDisplayName(modelTypeEnum)

	// ── Step 5: Load examples ──
	var examples []interface{} // will hold SpiderExample or BirdExample
	var totalCount int
	var datasetSize int // total size before slicing

	switch *benchmark {
	case "spider":
		spiderExamples, err := loadSpiderDev(devPath)
		if err != nil {
			log.Fatalf("Failed to load dev.json: %v", err)
		}
		datasetSize = len(spiderExamples)
		// Apply range
		if *endIdx == -1 || *endIdx > len(spiderExamples) {
			*endIdx = len(spiderExamples)
		}
		spiderExamples = spiderExamples[*startIdx:*endIdx]
		// Apply limit
		if *limit > 0 && *limit < len(spiderExamples) {
			spiderExamples = spiderExamples[:*limit]
		}
		totalCount = len(spiderExamples)
		for _, e := range spiderExamples {
			examples = append(examples, e)
		}

	case "bird":
		birdExamples, err := loadBirdDev(devPath)
		if err != nil {
			log.Fatalf("Failed to load dev.json: %v", err)
		}
		// Filter difficulty
		if *difficulty != "" {
			filtered := []BirdExample{}
			for _, ex := range birdExamples {
				if ex.Difficulty == *difficulty {
					filtered = append(filtered, ex)
				}
			}
			birdExamples = filtered
		}
		datasetSize = len(birdExamples)
		// Apply range
		if *endIdx == -1 || *endIdx > len(birdExamples) {
			*endIdx = len(birdExamples)
		}
		birdExamples = birdExamples[*startIdx:*endIdx]
		// Apply limit
		if *limit > 0 && *limit < len(birdExamples) {
			birdExamples = birdExamples[:*limit]
		}
		totalCount = len(birdExamples)
		for _, e := range birdExamples {
			examples = append(examples, e)
		}
	}

	// ── Step 5.5: Interactive range selection (if no range flags provided) ──
	noRangeFlags := *limit == 0 && *startIdx == 0 && *endIdx == datasetSize
	if noRangeFlags && *mode == "" {
		fmt.Println()
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Printf("📏 Select Evaluation Range (total: %d examples)\n", datasetSize)
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("  a. all         — Run all examples")
		fmt.Println("  N              — Run first N examples (e.g. 10)")
		fmt.Println("  N-M            — Run examples from index N to M (e.g. 0-49)")
		fmt.Println("  #N             — Run single example at index N (e.g. #42)")
		fmt.Println()
		fmt.Print("Enter choice [a]: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if input == "" || input == "a" || input == "all" {
			// keep all — no change
		} else if strings.HasPrefix(input, "#") {
			// single example: #N
			var idx int
			if _, err := fmt.Sscanf(input[1:], "%d", &idx); err != nil || idx < 0 || idx >= datasetSize {
				log.Fatalf("Invalid index: %s (valid range: 0-%d)", input, datasetSize-1)
			}
			examples = []interface{}{examples[idx]}
			totalCount = 1
		} else if strings.Contains(input, "-") {
			// range: N-M
			parts := strings.SplitN(input, "-", 2)
			var s, e int
			if _, err := fmt.Sscanf(parts[0], "%d", &s); err != nil {
				log.Fatalf("Invalid start index: %s", parts[0])
			}
			if _, err := fmt.Sscanf(parts[1], "%d", &e); err != nil {
				log.Fatalf("Invalid end index: %s", parts[1])
			}
			if s < 0 || e >= datasetSize || s > e {
				log.Fatalf("Invalid range: %d-%d (valid range: 0-%d)", s, e, datasetSize-1)
			}
			examples = examples[s : e+1]
			totalCount = len(examples)
		} else {
			// limit: first N
			var n int
			if _, err := fmt.Sscanf(input, "%d", &n); err != nil || n <= 0 {
				log.Fatalf("Invalid number: %s", input)
			}
			if n > len(examples) {
				n = len(examples)
			}
			examples = examples[:n]
			totalCount = n
		}
	}

	if totalCount == 0 {
		log.Fatalf("No examples to evaluate!")
	}

	// ── Step 6: Create output directory ──
	if *outputDir == "" {
		timestamp := time.Now().Format("20060102_150405")
		*outputDir = filepath.Join("results", *benchmark, fmt.Sprintf("%s_%s", timestamp, selectedMode.Name))
	}

	// ── Step 7: Print config summary ──
	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("🚀 %s Evaluation — %s\n", strings.ToUpper(*benchmark), selectedMode.Name)
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Benchmark:      %s\n", *benchmark)
	fmt.Printf("  Mode:           %s\n", selectedMode.Name)
	fmt.Printf("  Model:          %s\n", modelDisplayName)
	if totalCount != datasetSize {
		fmt.Printf("  Examples:       %d / %d\n", totalCount, datasetSize)
	} else {
		fmt.Printf("  Examples:       %d\n", totalCount)
	}
	fmt.Printf("  Log Mode:       %s\n", *logMode)
	fmt.Printf("  Output:         %s\n", *outputDir)
	fmt.Println("  ─────────────────────────────────────────────")
	fmt.Printf("  Use ReAct:      %v\n", selectedMode.UseReact)
	fmt.Printf("  Rich Context:   %v\n", selectedMode.UseRichContext)
	fmt.Printf("  React Linking:  %v\n", selectedMode.ReactLinking)
	fmt.Printf("  Clarify Mode:   %s\n", selectedMode.EnableClarify)
	fmt.Printf("  Proofread:      %v\n", selectedMode.EnableProofread)
	if *difficulty != "" {
		fmt.Printf("  Difficulty:     %s\n", *difficulty)
	}
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	// ── Step 8: Initialize LLM ──
	llmModel, err := llm.CreateLLMByType(modelTypeEnum)
	if err != nil {
		log.Fatalf("Failed to create LLM: %v", err)
	}

	// Ask model identity
	fmt.Println("🤖 Asking LLM to identify itself...")
	identityPrompt := "Please identify yourself. Output in the format: Model: [your model name and version]"
	identityResponse, err := llmModel.Call(context.Background(), identityPrompt)
	if err != nil {
		fmt.Printf("⚠️  Failed to get model identity: %v\n", err)
		fmt.Printf("📋 Using configured model name: %s\n\n", modelDisplayName)
	} else {
		fmt.Printf("🤖 %s\n\n", strings.TrimSpace(identityResponse))
	}

	// ── Step 9: Create output files ──
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output dir: %v", err)
	}

	// Create logs subdirectory for per-example logs
	logsDir := filepath.Join(*outputDir, "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		log.Fatalf("Failed to create logs dir: %v", err)
	}

	// Create inference.log (compressed summary log)
	inferenceLogPath := filepath.Join(*outputDir, "inference.log")
	inferenceLogFile, err := os.Create(inferenceLogPath)
	if err != nil {
		log.Fatalf("Failed to create inference.log: %v", err)
	}
	defer inferenceLogFile.Close()

	// Create shared inference logger
	evalLogger := inference.NewInferenceLogger()

	jsonPath := filepath.Join(*outputDir, "results.json")
	jsonFile, err := os.Create(jsonPath)
	if err != nil {
		log.Fatalf("Failed to create json file: %v", err)
	}
	defer jsonFile.Close()

	sqlPath := filepath.Join(*outputDir, "predict.sql")
	sqlFile, err := os.Create(sqlPath)
	if err != nil {
		log.Fatalf("Failed to create sql file: %v", err)
	}
	defer sqlFile.Close()

	// Write JSON array start
	jsonFile.WriteString("[\n")
	var jsonTailPos int64 // track position before the closing ']' for overwrite

	// ── Step 10: Redirect stdout to log.txt ──
	logTxtPath := filepath.Join(*outputDir, "log.txt")
	logTxtFile, err := os.Create(logTxtPath)
	if err != nil {
		log.Fatalf("Failed to create log.txt: %v", err)
	}
	defer logTxtFile.Close()

	// Save original stdout
	origStdout := os.Stdout
	// Redirect stdout to log.txt
	os.Stdout = logTxtFile

	// Restore stdout on exit
	defer func() {
		os.Stdout = origStdout
	}()

	// Print confirmation to original stdout (terminal)
	fmt.Fprintln(origStdout, "")
	fmt.Fprintln(origStdout, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintf(origStdout, "📝 Output redirected to: %s\n", logTxtPath)
	fmt.Fprintln(origStdout, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Fprintln(origStdout, "")

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⚠️  Received interrupt signal, closing files gracefully...")
		// Close per-example log if still open
		evalLogger.CloseFile()
		// JSON is already valid (closed with ] after each write), just sync & close
		jsonFile.Sync()
		jsonFile.Close()
		// Flush and close SQL file
		sqlFile.Sync()
		sqlFile.Close()
		// Flush and close inference log
		inferenceLogFile.Sync()
		inferenceLogFile.Close()
		// Flush and close log.txt
		logTxtFile.Sync()
		logTxtFile.Close()
		// Restore stdout
		os.Stdout = origStdout
		os.Exit(0)
	}()

	// ── Step 11: Run evaluation ──
	var (
		successCount  int
		totalTime     float64
		totalLLMCalls int
		totalTokens   int
		totalClarify  int
	)
	ctx := context.Background()

	// Memory tracking
	logMemory("Initial", 0)

	for i, example := range examples {
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

		var result EvalResult
		var exampleDbID, exampleQuestion, exampleGoldSQL string
		var exampleEvidence string

		switch e := example.(type) {
		case SpiderExample:
			exampleDbID = e.DbID
			exampleQuestion = e.Question
			exampleGoldSQL = e.Query
		case BirdExample:
			exampleDbID = e.DbID
			exampleQuestion = e.Question
			exampleGoldSQL = e.SQL
			exampleEvidence = e.Evidence
		}

		// ── Create per-example log file ──
		logFileName := fmt.Sprintf("%04d_%s.log", i+1, exampleDbID)
		logFilePath := filepath.Join(logsDir, logFileName)
		logFile, logErr := os.Create(logFilePath)
		if logErr != nil {
			log.Printf("Warning: Failed to create log file %s: %v", logFilePath, logErr)
		} else {
			evalLogger.SetFile(logFile)
			// Write log header
			evalLogger.FileOnly("========================================\n")
			evalLogger.FileOnly("Example: %04d\n", i+1)
			evalLogger.FileOnly("DB: %s\n", exampleDbID)
			evalLogger.FileOnly("Question: %s\n", exampleQuestion)
			evalLogger.FileOnly("Gold SQL: %s\n", exampleGoldSQL)
			if exampleEvidence != "" {
				evalLogger.FileOnly("Evidence: %s\n", exampleEvidence)
			}
			evalLogger.FileOnly("Mode: %s\n", selectedMode.Name)
			evalLogger.FileOnly("========================================\n\n")
		}

		switch e := example.(type) {
		case SpiderExample:
			fmt.Printf("[%d/%d] DB: %s\n", i+1, totalCount, e.DbID)
			fmt.Printf("Question: %s\n", e.Question)
			fmt.Printf("Gold SQL: %s\n", e.Query)
			result = evaluateSpider(ctx, llmModel, e, dbDir, contextDir, selectedMode, *logMode, evalLogger)

		case BirdExample:
			fmt.Printf("[%d/%d] DB: %s (difficulty: %s)\n", i+1, totalCount, e.DbID, e.Difficulty)
			fmt.Printf("Question: %s\n", e.Question)
			if e.Evidence != "" {
				fmt.Printf("Evidence: %s\n", e.Evidence)
			}
			fmt.Printf("Gold SQL: %s\n", e.SQL)
			result = evaluateBird(ctx, llmModel, e, dbDir, contextDir, selectedMode, *logMode, evalLogger)
		}

		// Update stats
		if result.Status == "success" {
			successCount++
		}
		totalTime += result.TimeSeconds
		totalLLMCalls += result.LLMCalls
		totalTokens += result.TotalTokens
		totalClarify += result.ClarifyCount

		// Incremental JSON write (always keep file as valid JSON)
		if i > 0 {
			// Seek back to overwrite the previous closing '\n]\n'
			jsonFile.Seek(jsonTailPos, 0)
			jsonFile.WriteString(",\n")
		}
		jsonData, err := json.MarshalIndent(result, "  ", "  ")
		if err != nil {
			log.Printf("Failed to marshal result: %v", err)
		} else {
			jsonFile.WriteString("  " + string(jsonData))
			jsonTailPos, _ = jsonFile.Seek(0, 1) // remember position before ']'
			jsonFile.WriteString("\n]\n")
			jsonFile.Sync()
		}

		// Incremental SQL write (with Sync for crash safety)
		sql := result.GeneratedSQL
		if sql == "" {
			sql = "SELECT 1"
		}
		sql = strings.TrimSpace(sql)
		sql = strings.TrimSuffix(sql, ";")
		sql = strings.ReplaceAll(sql, "\n", " ")
		sql = strings.ReplaceAll(sql, "\r", " ")
		sql = strings.Join(strings.Fields(sql), " ")
		fmt.Fprintf(sqlFile, "%s\t%s\n", sql, result.DbID)
		sqlFile.Sync()

		// Print result
		fmt.Printf("Generated: %s\n", result.GeneratedSQL)
		fmt.Printf("Status: %s\n", result.Status)
		if result.Error != "" {
			fmt.Printf("Error: %s\n", result.Error)
		}
		fmt.Printf("Time: %.2fs\n", result.TimeSeconds)
		fmt.Printf("LLM Calls: %d, Tokens: %d\n", result.LLMCalls, result.TotalTokens)
		if result.ClarifyCount > 0 {
			fmt.Printf("Clarify Count: %d\n", result.ClarifyCount)
		}

		// ── Write per-example log footer & close ──
		if logFile != nil {
			evalLogger.FileOnly("\n[Result]\n")
			evalLogger.FileOnly("  Generated SQL: %s\n", result.GeneratedSQL)
			evalLogger.FileOnly("  Status: %s\n", result.Status)
			if result.Error != "" {
				evalLogger.FileOnly("  Error: %s\n", result.Error)
			}
			evalLogger.FileOnly("  Time: %.2fs\n", result.TimeSeconds)
			evalLogger.FileOnly("  LLM Calls: %d, Tokens: %d\n", result.LLMCalls, result.TotalTokens)
			evalLogger.CloseFile()
		}

		// ── Write inference.log compressed entry ──
		statusIcon := "✅"
		if result.Status != "success" {
			statusIcon = "❌"
		}
		tablesStr := strings.Join(result.SelectedTables, ", ")
		fmt.Fprintf(inferenceLogFile, "[%04d] %s | Q: %s\n", i+1, result.DbID, result.Question)
		fmt.Fprintf(inferenceLogFile, "       Tables: [%s] | Iters: %d | Time: %.1fs | %s\n", tablesStr, result.LLMCalls, result.TimeSeconds, statusIcon)
		fmt.Fprintf(inferenceLogFile, "       Gold: %s\n", result.GoldSQL)
		fmt.Fprintf(inferenceLogFile, "       Pred: %s\n", result.GeneratedSQL)
		if result.Error != "" {
			fmt.Fprintf(inferenceLogFile, "       Error: %s\n", result.Error)
		}
		fmt.Fprintf(inferenceLogFile, "\n")
		inferenceLogFile.Sync()

		// GC after each sample
		runtime.GC()

		// Memory report after EVERY sample (with RSS)
		logMemory("After", i+1)
	}

	// JSON array is already properly closed after each iteration (crash-safe)

	// ── Step 11: Print summary (to both log.txt and terminal) ──
	// Helper to print to both log file and terminal
	both := func(format string, a ...interface{}) {
		fmt.Printf(format, a...)
		fmt.Fprintf(origStdout, format, a...)
	}

	both("\n")
	both("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	both("📊 Evaluation Summary\n")
	both("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	both("Benchmark: %s | Mode: %s | Model: %s\n", *benchmark, selectedMode.Name, modelDisplayName)
	both("Total: %d\n", totalCount)
	both("Success: %d (%.1f%%)\n", successCount, float64(successCount)/float64(totalCount)*100)
	both("Failed: %d\n", totalCount-successCount)
	if totalCount > 0 {
		both("Avg Time: %.2fs\n", totalTime/float64(totalCount))
		both("Avg LLM Calls: %.1f\n", float64(totalLLMCalls)/float64(totalCount))
		both("Total Tokens: %d (Avg: %d per query)\n", totalTokens, totalTokens/totalCount)
	}
	if totalClarify > 0 {
		both("Total Clarifications: %d (%.1f%%)\n", totalClarify, float64(totalClarify)/float64(totalCount)*100)
	}

	// Get absolute path for output dir
	absOutputDir, _ := filepath.Abs(*outputDir)

	both("\n✅ Results saved to: %s/\n", *outputDir)
	both("  - results.json     (detailed results with ReAct steps)\n")
	both("  - predict.sql      (predicted SQL for official evaluation)\n")
	both("  - inference.log    (compressed summary log)\n")
	both("  - log.txt          (full inference output)\n")
	both("  - logs/            (per-example full logs, %d files)\n", totalCount)
	both("\n")
	both("📂 Quick access:\n")
	both("  cd %s\n", absOutputDir)
	both("  cat inference.log                    # overview\n")
	both("  cat logs/0001_*.log                  # single example\n")
	both("  grep '❌' inference.log              # failed examples\n")
	both("  tail -f log.txt                      # watch live output\n")
}

// ─────────────────────────────────────────────────────
// Spider evaluation
// ─────────────────────────────────────────────────────

func evaluateSpider(
	ctx context.Context,
	llm llms.Model,
	example SpiderExample,
	dbDir string,
	contextDir string,
	mode EvalMode,
	logMode string,
	logger *inference.InferenceLogger,
) (result EvalResult) {
	result = EvalResult{
		DbID:     example.DbID,
		Question: example.Question,
		GoldSQL:  example.Query,
		Status:   "error",
	}

	startTime := time.Now()
	defer func() {
		result.TimeSeconds = time.Since(startTime).Seconds()
	}()

	// Create adapter
	dbPath := filepath.Join(dbDir, example.DbID, example.DbID+".sqlite")
	dbAdapter, err := adapter.NewAdapter(&adapter.DBConfig{
		Type:     "sqlite",
		FilePath: dbPath,
	})
	if err != nil {
		result.Error = fmt.Sprintf("create adapter: %v", err)
		return result
	}
	if err := dbAdapter.Connect(ctx); err != nil {
		result.Error = fmt.Sprintf("connect db: %v", err)
		return result
	}
	defer dbAdapter.Close()

	// Context file
	var contextFile string
	if mode.UseRichContext {
		contextFile = filepath.Join(contextDir, example.DbID+".json")
		if _, err := os.Stat(contextFile); os.IsNotExist(err) {
			result.Error = fmt.Sprintf("context file not found: %s", contextFile)
			return result
		}
	}

	// Pipeline
	pipelineConfig := &inference.Config{
		UseRichContext:          mode.UseRichContext,
		UseReact:                mode.UseReact,
		ReactLinking:            mode.ReactLinking,
		UseDryRun:               false,
		MaxIterations:           20,
		ContextFile:             contextFile,
		ClarifyMode:             mode.EnableClarify,
		LogMode:                 logMode,
		ResultFields:            example.ResultFields,
		ResultFieldsDescription: example.ResultFieldsDescription,
		EnableProofread:         mode.EnableProofread,
		DBName:                  example.DbID,
		DBType:                  "sqlite",
	}

	pipeline := inference.NewPipeline(llm, dbAdapter, pipelineConfig)
	if logger != nil {
		pipeline.SetLogger(logger)
	}
	inferResult, err := pipeline.Execute(ctx, example.Question)
	if err != nil {
		result.Error = fmt.Sprintf("inference: %v", err)
		return result
	}

	result.GeneratedSQL = inferResult.GeneratedSQL
	result.LLMCalls = inferResult.LLMCalls
	result.TotalTokens = inferResult.TotalTokens
	result.ClarifyCount = inferResult.ClarifyCount
	result.SelectedTables = inferResult.SelectedTables
	result.ReActSteps = inferResult.ReActSteps
	result.Status = "success"
	return result
}

// ─────────────────────────────────────────────────────
// BIRD evaluation
// ─────────────────────────────────────────────────────

func evaluateBird(
	ctx context.Context,
	llm llms.Model,
	example BirdExample,
	dbDir string,
	contextDir string,
	mode EvalMode,
	logMode string,
	logger *inference.InferenceLogger,
) (result EvalResult) {
	result = EvalResult{
		QuestionID: example.QuestionID,
		DbID:       example.DbID,
		Question:   example.Question,
		Evidence:   example.Evidence,
		GoldSQL:    example.SQL,
		Status:     "error",
		Difficulty: example.Difficulty,
	}

	startTime := time.Now()
	defer func() {
		result.TimeSeconds = time.Since(startTime).Seconds()
	}()

	// Create adapter
	dbPath := filepath.Join(dbDir, example.DbID, example.DbID+".sqlite")
	dbAdapter, err := adapter.NewAdapter(&adapter.DBConfig{
		Type:     "sqlite",
		FilePath: dbPath,
	})
	if err != nil {
		result.Error = fmt.Sprintf("create adapter: %v", err)
		return result
	}
	if err := dbAdapter.Connect(ctx); err != nil {
		result.Error = fmt.Sprintf("connect db: %v", err)
		return result
	}
	defer dbAdapter.Close()

	// Context file
	var contextFile string
	if mode.UseRichContext {
		contextFile = filepath.Join(contextDir, example.DbID+".json")
		if _, err := os.Stat(contextFile); os.IsNotExist(err) {
			// BIRD may not have rich context for all DBs, skip silently
			contextFile = ""
		}
	}

	// Build full question with evidence
	question := example.Question
	if example.Evidence != "" {
		question = fmt.Sprintf("%s\nEvidence: %s", example.Question, example.Evidence)
	}

	// Pipeline
	pipelineConfig := &inference.Config{
		UseRichContext: mode.UseRichContext && contextFile != "",
		UseReact:       mode.UseReact,
		ReactLinking:   mode.ReactLinking,
		UseDryRun:      false,
		MaxIterations:  20,
		ContextFile:    contextFile,
		ClarifyMode:    mode.EnableClarify,
		LogMode:        logMode,
		DBName:         example.DbID,
		DBType:         "sqlite",
	}

	pipeline := inference.NewPipeline(llm, dbAdapter, pipelineConfig)
	if logger != nil {
		pipeline.SetLogger(logger)
	}
	inferResult, err := pipeline.Execute(ctx, question)
	if err != nil {
		result.Error = fmt.Sprintf("inference: %v", err)
		return result
	}

	result.GeneratedSQL = inferResult.GeneratedSQL
	result.LLMCalls = inferResult.LLMCalls
	result.TotalTokens = inferResult.TotalTokens
	result.ClarifyCount = inferResult.ClarifyCount
	result.SelectedTables = inferResult.SelectedTables
	result.ReActSteps = inferResult.ReActSteps
	result.Status = "success"
	return result
}

// ─────────────────────────────────────────────────────
// Loaders
// ─────────────────────────────────────────────────────

func loadSpiderDev(path string) ([]SpiderExample, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var examples []SpiderExample
	if err := json.Unmarshal(data, &examples); err != nil {
		return nil, err
	}
	return examples, nil
}

func loadBirdDev(path string) ([]BirdExample, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var examples []BirdExample
	if err := json.Unmarshal(data, &examples); err != nil {
		return nil, err
	}
	return examples, nil
}

// getProcessRSSMB reads the real RSS (Resident Set Size) from /proc/self/status.
// This captures memory allocated by CGo (e.g. go-sqlite3) that Go's runtime.ReadMemStats cannot see.
func getProcessRSSMB() int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				var kb int64
				if _, err := fmt.Sscanf(fields[1], "%d", &kb); err == nil {
					return kb / 1024 // return MB
				}
			}
		}
	}
	return -1
}

// logMemory prints a memory report line with both Go heap and OS RSS.
func logMemory(label string, sampleIdx int) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	rssMB := getProcessRSSMB()
	fmt.Printf("[Memory] %s #%d — GoHeap: %d MB, HeapInUse: %d MB, GoSys: %d MB, RSS: %d MB, GC: %d\n",
		label, sampleIdx, m.Alloc/1024/1024, m.HeapInuse/1024/1024, m.Sys/1024/1024, rssMB, m.NumGC)
}

func parseModelType(modelType string) llm.ModelType {
	switch modelType {
	case "deepseek-v3":
		return llm.ModelDeepSeekV3
	case "deepseek-v3.2":
		return llm.ModelDeepSeekV32
	case "qwen-max":
		return llm.ModelQwenMax
	case "qwen3-max":
		return llm.ModelQwen3Max
	case "ali-deepseek-v3.2":
		return llm.ModelAliDeepSeekV32
	default:
		log.Fatalf("Unknown model type: %s. Available: deepseek-v3, deepseek-v3.2, qwen-max, qwen3-max, ali-deepseek-v3.2", modelType)
		return ""
	}
}
