package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"reactsql/internal/adapter"
	"reactsql/internal/agent"
	contextpkg "reactsql/internal/context"
	"reactsql/internal/inference"
	"reactsql/internal/llm"
)

// ─────────────────────────────────────────────────────
// ANSI color helpers
// ─────────────────────────────────────────────────────

const (
	reset   = "\033[0m"
	bold    = "\033[1m"
	dim     = "\033[2m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
	white   = "\033[37m"
)

func header(title string) {
	line := strings.Repeat("━", 60)
	fmt.Printf("\n%s%s%s\n", cyan+bold, line, reset)
	fmt.Printf("%s  %s%s\n", cyan+bold, title, reset)
	fmt.Printf("%s%s%s\n\n", cyan+bold, line, reset)
}

func subHeader(title string) {
	fmt.Printf("\n%s── %s ──%s\n\n", yellow+bold, title, reset)
}

func info(label, value string) {
	fmt.Printf("  %s%-20s%s %s\n", dim, label, reset, value)
}

func success(msg string) {
	fmt.Printf("  %s✓%s %s\n", green, reset, msg)
}

func warn(msg string) {
	fmt.Printf("  %s⚠%s %s\n", yellow, reset, msg)
}

func codeBlock(title, content string) {
	fmt.Printf("\n%s┌─ %s%s\n", blue, title, reset)
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		fmt.Printf("%s│%s %s\n", blue, reset, line)
	}
	fmt.Printf("%s└─%s\n", blue, reset)
}

// ─────────────────────────────────────────────────────
// Benchmark question types
// ─────────────────────────────────────────────────────

type spiderQuestion struct {
	DBID     string `json:"db_id"`
	Question string `json:"question"`
	Query    string `json:"query"`
}

type birdQuestion struct {
	QuestionID int    `json:"question_id"`
	DBID       string `json:"db_id"`
	Question   string `json:"question"`
	Evidence   string `json:"evidence"`
	SQL        string `json:"SQL"`
	Difficulty string `json:"difficulty"`
}

// ─────────────────────────────────────────────────────
// Main
// ─────────────────────────────────────────────────────

func main() {
	// Flags
	benchmark := flag.String("benchmark", "spider", "Benchmark: spider | bird")
	dbName := flag.String("db", "concert_singer", "Database name (db_id from benchmark)")
	maxQuestions := flag.Int("n", 10, "Max number of questions to test")
	withLLM := flag.Bool("with-llm", false, "Actually run LLM for Schema Linking + SQL generation")
	modelType := flag.String("model", "deepseek-v3", "Model type (only used with --with-llm)")
	regenRC := flag.Bool("regen-rc", false, "Regenerate Rich Context from scratch (requires LLM)")
	showPrompt := flag.Bool("show-prompt", true, "Show the full SQL generation prompt")
	flag.Parse()

	header("End-to-End Pipeline Visualization")
	info("Benchmark:", *benchmark)
	info("Database:", *dbName)
	info("Max questions:", fmt.Sprintf("%d", *maxQuestions))
	info("With LLM:", fmt.Sprintf("%v", *withLLM))
	info("Regen RC:", fmt.Sprintf("%v", *regenRC))

	// ── Resolve paths ──
	var devFile, dbDir, contextDir string
	switch *benchmark {
	case "spider":
		devFile = "benchmarks/spider/dev.json"
		dbDir = "benchmarks/spider/database"
		contextDir = "contexts/sqlite/spider"
	case "bird":
		devFile = "benchmarks/bird/dev/dev.json"
		dbDir = "benchmarks/bird/dev/dev_databases"
		contextDir = "contexts/sqlite/bird"
	default:
		log.Fatalf("Unknown benchmark: %s", *benchmark)
	}

	dbPath := filepath.Join(dbDir, *dbName, *dbName+".sqlite")
	contextFile := filepath.Join(contextDir, *dbName+".json")

	info("DB path:", dbPath)
	info("Context file:", contextFile)

	// Validate DB exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		log.Fatalf("Database not found: %s", dbPath)
	}

	// ── Step 1: Connect to DB ──
	ctx := context.Background()

	dbAdapter, err := adapter.NewAdapter(&adapter.DBConfig{
		Type:     "sqlite",
		FilePath: dbPath,
	})
	if err != nil {
		log.Fatalf("Failed to create adapter: %v", err)
	}
	if err := dbAdapter.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer dbAdapter.Close()
	success("Connected to database")

	// ── Step 2: Load or regenerate Rich Context ──
	var sharedCtx *contextpkg.SharedContext

	if *regenRC {
		header("Phase: Rich Context Generation (fresh)")
		sharedCtx, err = regenerateRC(ctx, *dbName, dbDir, *modelType, dbAdapter)
		if err != nil {
			log.Fatalf("RC generation failed: %v", err)
		}
		// Save to temp location for inspection
		tmpFile := filepath.Join(os.TempDir(), *dbName+"_e2e_test.json")
		if err := sharedCtx.SaveToFile(tmpFile); err == nil {
			info("Saved new RC to:", tmpFile)
		}
	} else {
		// Load existing context
		if _, err := os.Stat(contextFile); os.IsNotExist(err) {
			warn(fmt.Sprintf("Context file not found: %s", contextFile))
			warn("Run with --regen-rc to generate, or generate via gen_all_dev first")
			warn("Continuing without Rich Context...")
		} else {
			data, err := os.ReadFile(contextFile)
			if err != nil {
				log.Fatalf("Failed to read context: %v", err)
			}
			sharedCtx = &contextpkg.SharedContext{}
			if err := json.Unmarshal(data, sharedCtx); err != nil {
				log.Fatalf("Failed to parse context: %v", err)
			}
			success(fmt.Sprintf("Loaded Rich Context (%d tables)", len(sharedCtx.Tables)))
		}
	}

	// ── Step 2.5: Run QualityChecker (deterministic, always fresh) ──
	if sharedCtx != nil {
		header("Phase: Deterministic Quality Check (Phase 1.5)")
		for tableName := range sharedCtx.Tables {
			qc := contextpkg.NewQualityChecker(dbAdapter, sharedCtx, tableName)
			if err := qc.RunAll(ctx); err != nil {
				warn(fmt.Sprintf("QualityChecker failed for %s: %v", tableName, err))
			}
		}
		// Show quality issues
		totalIssues := 0
		for tableName, table := range sharedCtx.Tables {
			if len(table.QualityIssues) > 0 {
				fmt.Printf("  %s%s%s:\n", bold, tableName, reset)
				for _, qi := range table.QualityIssues {
					severity := qi.Severity
					color := dim
					switch severity {
					case "critical":
						color = red
					case "warning":
						color = yellow
					}
					fmt.Printf("    %s[%s]%s %s.%s: %s → %s%s%s\n",
						color, severity, reset, qi.Table, qi.Column, qi.Description, cyan, qi.SQLFix, reset)
					totalIssues++
				}
			}
			// Show value stats summary
			statsCount := 0
			for _, col := range table.Columns {
				if col.ValueStats != nil {
					statsCount++
				}
			}
			if statsCount > 0 {
				fmt.Printf("    %s(%d columns with value statistics)%s\n", dim, statsCount, reset)
			}
		}
		if totalIssues == 0 {
			success("No quality issues detected")
		} else {
			warn(fmt.Sprintf("Found %d quality issues across all tables", totalIssues))
		}
	}

	// ── Step 3: Show Rich Context (Compact Prompt) ──
	if sharedCtx != nil {
		header("Phase: Exported Rich Context (Compact Prompt)")

		opts := &contextpkg.ExportOptions{
			IncludeColumns:     true,
			IncludeIndexes:     true,
			IncludeRichContext: true,
			IncludeStats:       true,
		}
		compactPrompt := sharedCtx.ExportToCompactPrompt(opts)
		codeBlock("Compact Prompt (all tables)", compactPrompt)
	}

	// ── Step 4: Load benchmark questions ──
	header("Phase: Benchmark Questions")

	questions, goldSQLs := loadQuestions(devFile, *benchmark, *dbName, *maxQuestions)
	if len(questions) == 0 {
		warn("No questions found for this database")
		return
	}
	success(fmt.Sprintf("Loaded %d questions for %s", len(questions), *dbName))
	fmt.Println()
	for i, q := range questions {
		fmt.Printf("  %s%2d.%s %s\n", bold, i+1, reset, q)
		if goldSQLs[i] != "" {
			fmt.Printf("      %sGold:%s %s\n", dim, reset, goldSQLs[i])
		}
	}

	// ── Step 5: Schema Linking + Prompt Visualization ──
	if !*showPrompt {
		return
	}

	header("Phase: Schema Linking + Prompt Assembly")

	// Extract table info from context (or DB)
	var allTableInfo map[string]*inference.TableInfo
	if sharedCtx != nil {
		allTableInfo = inference.ExtractTableInfo(sharedCtx)
	}

	// Show what Schema Linker sees
	if allTableInfo != nil {
		subHeader("Schema Linker Input (all tables)")
		for name, ti := range allTableInfo {
			fmt.Printf("  %s%s%s — %d columns\n", bold, name, reset, len(ti.Columns))
			if ti.Description != "" {
				fmt.Printf("    %sDesc:%s %s\n", dim, reset, ti.Description)
			}
			if ti.QualitySummary != "" {
				fmt.Printf("    %s%s%s\n", yellow, ti.QualitySummary, reset)
			}
		}
	}

	// Build cross-table quality summary
	var crossTableSummary string
	if sharedCtx != nil {
		allTables := make([]string, 0, len(sharedCtx.Tables))
		for name := range sharedCtx.Tables {
			allTables = append(allTables, name)
		}
		crossTableSummary = sharedCtx.BuildCrossTableQualitySummary(allTables)
		if crossTableSummary != "" {
			subHeader("Cross-Table Quality Summary")
			fmt.Print("  " + strings.ReplaceAll(crossTableSummary, "\n", "\n  "))
			fmt.Println()
		} else {
			info("Cross-table summary:", "(none — no cross-table JOIN issues)")
		}
	}

	// For each question, build and show the prompt
	for i, question := range questions {
		subHeader(fmt.Sprintf("Question %d/%d: %s", i+1, len(questions), question))

		if *withLLM {
			// Actually run the full pipeline
			runWithLLM(ctx, question, sharedCtx, dbAdapter, *benchmark, *dbName, *modelType, goldSQLs[i])
		} else {
			// Show the prompt that would be sent to the LLM (all tables selected)
			showMockPrompt(question, sharedCtx, *benchmark, crossTableSummary)
		}

		if i >= *maxQuestions-1 {
			break
		}
	}

	header("Done")
}

// ─────────────────────────────────────────────────────
// RC regeneration
// ─────────────────────────────────────────────────────

func regenerateRC(ctx context.Context, dbName, dbDir, modelType string, dbAdapter adapter.DBAdapter) (*contextpkg.SharedContext, error) {
	sharedCtx := contextpkg.NewSharedContext(dbName, "sqlite")

	// Load schema.sql if available
	schemaPath := filepath.Join(dbDir, dbName, "schema.sql")
	if _, err := os.Stat(schemaPath); err == nil {
		if err := sharedCtx.LoadSchemaFromFile(schemaPath); err != nil {
			warn(fmt.Sprintf("Failed to load schema.sql: %v", err))
		}
	}

	// Create LLM
	model := parseModelType(modelType)
	llmInstance, err := llm.CreateLLMByType(model)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM: %w", err)
	}

	// Phase 1: Coordinator discovers tables
	subHeader("Coordinator: Discovering tables")
	coordinator, err := agent.NewCoordinatorAgent("coordinator", llmInstance, dbAdapter, sharedCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to create coordinator: %w", err)
	}
	if err := coordinator.Execute(ctx); err != nil {
		return nil, fmt.Errorf("coordinator failed: %w", err)
	}

	tasks := sharedCtx.GetAllTasks()
	var workerTasks []*contextpkg.TaskInfo
	for _, task := range tasks {
		if task.AgentID != "coordinator" {
			workerTasks = append(workerTasks, task)
		}
	}
	success(fmt.Sprintf("Found %d tables", len(workerTasks)))

	// Phase 1 + 1.5 + 2 + 3: Worker agents (sequential for visibility)
	for _, task := range workerTasks {
		tableName := task.ID[8:] // strip "analyze_"
		subHeader(fmt.Sprintf("Worker: %s", tableName))

		worker, err := agent.NewWorkerAgent(task.AgentID, task.ID, tableName, llmInstance, dbAdapter, sharedCtx)
		if err != nil {
			warn(fmt.Sprintf("Failed to create worker: %v", err))
			continue
		}
		if err := worker.Execute(ctx); err != nil {
			warn(fmt.Sprintf("Worker failed: %v", err))
		} else {
			success(fmt.Sprintf("Table %s analyzed", tableName))
		}
	}

	// Analyze JOIN paths
	sharedCtx.AnalyzeJoinPaths()

	return sharedCtx, nil
}

// ─────────────────────────────────────────────────────
// Run with real LLM
// ─────────────────────────────────────────────────────

func runWithLLM(ctx context.Context, question string, sharedCtx *contextpkg.SharedContext, dbAdapter adapter.DBAdapter, benchmark, dbName, modelType, goldSQL string) {
	model := parseModelType(modelType)
	llmInstance, err := llm.CreateLLMByType(model)
	if err != nil {
		warn(fmt.Sprintf("Failed to create LLM: %v", err))
		return
	}

	contextDir := fmt.Sprintf("contexts/sqlite/%s", benchmark)
	contextFile := filepath.Join(contextDir, dbName+".json")

	// Use temp file if regen'd, otherwise existing
	tmpFile := filepath.Join(os.TempDir(), dbName+"_e2e_test.json")
	if _, err := os.Stat(tmpFile); err == nil {
		contextFile = tmpFile
	}

	config := &inference.Config{
		UseRichContext: sharedCtx != nil,
		UseReact:       true,
		ReactLinking:   false,
		MaxIterations:  5,
		ContextFile:    contextFile,
		ClarifyMode:    "off",
		DBName:         dbName,
		DBType:         "SQLite",
		Benchmark:      benchmark,
	}

	pipeline := inference.NewPipeline(llmInstance, dbAdapter, config)
	logger := inference.NewInferenceLogger()
	pipeline.SetLogger(logger)

	result, err := pipeline.Execute(ctx, question)
	if err != nil {
		warn(fmt.Sprintf("Pipeline failed: %v", err))
		return
	}

	fmt.Printf("  %sSelected tables:%s %v\n", bold, reset, result.SelectedTables)
	fmt.Printf("  %sGenerated SQL:%s\n", bold, reset)
	fmt.Printf("    %s%s%s\n", green, result.GeneratedSQL, reset)
	if goldSQL != "" {
		fmt.Printf("  %sGold SQL:%s\n", bold, reset)
		fmt.Printf("    %s%s%s\n", dim, goldSQL, reset)
	}
	fmt.Printf("  %sLLM calls: %d | Time: %v%s\n", dim, result.LLMCalls, result.TotalTime, reset)
}

// ─────────────────────────────────────────────────────
// Show mock prompt (no LLM)
// ─────────────────────────────────────────────────────

func showMockPrompt(question string, sharedCtx *contextpkg.SharedContext, benchmark, crossTableSummary string) {
	if sharedCtx == nil {
		warn("No Rich Context — cannot show prompt")
		return
	}

	// Build compact prompt (all tables selected — simulates perfect Schema Linking)
	allTables := make([]string, 0, len(sharedCtx.Tables))
	for name := range sharedCtx.Tables {
		allTables = append(allTables, name)
	}

	opts := &contextpkg.ExportOptions{
		Tables:             allTables,
		IncludeColumns:     true,
		IncludeIndexes:     true,
		IncludeRichContext: true,
		IncludeStats:       true,
	}
	contextPrompt := sharedCtx.ExportToCompactPrompt(opts)

	// Build the full prompt using the same logic as Pipeline.buildPrompt
	var sb strings.Builder

	sb.WriteString("You are a SQL expert. Generate SQL to answer the question.\n\n")
	sb.WriteString("**Database Type: SQLite**\n")
	sb.WriteString("CRITICAL: Write SQL that strictly follows SQLite syntax rules.\n")
	sb.WriteString("Common syntax differences to watch:\n")
	sb.WriteString("- Use double quotes for identifiers if needed, single quotes for strings\n")
	sb.WriteString("- No LIMIT offset without LIMIT clause\n")
	sb.WriteString("- Use || for string concatenation\n\n")

	sb.WriteString("Database Schema:\n")
	sb.WriteString(contextPrompt)
	sb.WriteString("\n\n")

	// Cross-table quality summary
	if crossTableSummary != "" {
		sb.WriteString(crossTableSummary)
		sb.WriteString("\n")
	}

	// Join paths + field semantics
	if joinPaths := sharedCtx.FormatJoinPathsForPrompt(); joinPaths != "" {
		sb.WriteString(joinPaths)
	}
	if fieldSem := sharedCtx.FormatFieldSemanticsForPrompt(); fieldSem != "" {
		sb.WriteString(fieldSem)
	}

	sb.WriteString(`IMPORTANT: Rich Context may be outdated or incorrect. When Rich Context conflicts with actual database data, trust the database.

`)

	// Benchmark-specific best practices
	if benchmark == "bird" {
		sb.WriteString(`SQL Rules & Best Practices (BIRD):
1. EVIDENCE IS CRITICAL: The "Evidence" section contains exact column mappings, value constraints, and formulas.
   - If evidence says "X refers to Y = 'Z'" → you MUST use column Y with value 'Z'
   - If evidence gives a formula → use that exact formula
   - If evidence defines a threshold → use those exact bounds
   - NEVER ignore or reinterpret evidence constraints
2. Projection: Return ONLY the columns the question asks for — no extra columns
3. DISTINCT: Only use when question says "different"/"unique" or JOINs produce actual duplicates
4. Type Mismatch: Use CAST(field AS INTEGER/REAL) for TEXT columns storing numbers
5. Percentage/Rate: Always CAST(... AS REAL) to avoid integer division
6. IIF/CASE: For yes/no or conditional results, use IIF(condition, 'YES', 'NO')
7. Aggregation: Top N → ORDER BY DESC LIMIT N; Rate → CAST AS REAL * 100 / denom
8. NULL handling: NULL ≠ 0 ≠ ''. Filter with IS NOT NULL
9. Date handling: Use substr(date, 1, 10) or LIKE 'YYYY-MM-DD%'

`)
	} else {
		sb.WriteString(`SQL Rules & Best Practices:
1. Type Mismatch: Use CAST(field AS INTEGER/REAL), filter non-numeric
2. Whitespace: Use TRIM(field) in JOIN/WHERE/GROUP BY
3. NULL handling: NULL ≠ 0, filter with IS NOT NULL
4. String matching: Use exact values from Rich Context
5. Aggregation: Top N → ORDER BY DESC LIMIT N; Count by → GROUP BY; Rate → CAST AS REAL
6. Extreme values with ties: WHERE col = (SELECT MAX/MIN(col)...)
7. Duplicates: Consider DISTINCT with JOINs
8. Orphan records: Use LEFT JOIN if quality issues mention orphans
9. Value verification: Verify which column contains specific text values

`)
	}

	sb.WriteString(fmt.Sprintf("Question: %s\n\n", question))

	sb.WriteString(`Available Tools:
- execute_sql: Execute SQL and see results

Workflow:
1. Analyze question and schema
2. If string values missing from Rich Context → use execute_sql to find them
5. Write SQL following best practices
6. If uncertain → validate with execute_sql (use LIMIT/COUNT for large results)
7. Provide Final Answer
`)

	prompt := sb.String()

	// Count tokens (rough estimate: ~4 chars per token)
	charCount := len(prompt)
	approxTokens := charCount / 4

	fmt.Printf("  %sPrompt length:%s %d chars (~%d tokens)\n", dim, reset, charCount, approxTokens)

	// Show prompt with line numbers for easy reference
	lines := strings.Split(prompt, "\n")
	fmt.Printf("\n%s┌─ Full Prompt (%d lines) ─%s\n", blue, len(lines), reset)
	for i, line := range lines {
		// Highlight key sections
		lineColor := ""
		if strings.HasPrefix(line, "Database Schema:") || strings.HasPrefix(line, "Question:") ||
			strings.HasPrefix(line, "SQL Rules") || strings.HasPrefix(line, "Available Tools:") ||
			strings.HasPrefix(line, "Cross-Table") {
			lineColor = yellow + bold
		} else if strings.HasPrefix(line, "Table ") {
			lineColor = green
		} else if strings.Contains(line, "⚠") || strings.Contains(line, "[critical]") {
			lineColor = red
		} else if strings.Contains(line, "[warning]") {
			lineColor = yellow
		} else if strings.HasPrefix(line, "  Business Notes:") || strings.HasPrefix(line, "    *") {
			lineColor = magenta
		}

		if lineColor != "" {
			fmt.Printf("%s│%s %s%3d%s %s%s%s\n", blue, reset, dim, i+1, reset, lineColor, line, reset)
		} else {
			fmt.Printf("%s│%s %s%3d%s %s\n", blue, reset, dim, i+1, reset, line)
		}
	}
	fmt.Printf("%s└─%s\n", blue, reset)
}

// ─────────────────────────────────────────────────────
// Load questions from benchmark
// ─────────────────────────────────────────────────────

func loadQuestions(devFile, benchmark, dbName string, maxN int) ([]string, []string) {
	data, err := os.ReadFile(devFile)
	if err != nil {
		warn(fmt.Sprintf("Failed to read dev file: %v", err))
		return nil, nil
	}

	var questions []string
	var goldSQLs []string

	switch benchmark {
	case "spider":
		var entries []spiderQuestion
		if err := json.Unmarshal(data, &entries); err != nil {
			warn(fmt.Sprintf("Failed to parse: %v", err))
			return nil, nil
		}
		for _, e := range entries {
			if e.DBID == dbName {
				questions = append(questions, e.Question)
				goldSQLs = append(goldSQLs, e.Query)
				if len(questions) >= maxN {
					break
				}
			}
		}
	case "bird":
		var entries []birdQuestion
		if err := json.Unmarshal(data, &entries); err != nil {
			warn(fmt.Sprintf("Failed to parse: %v", err))
			return nil, nil
		}
		for _, e := range entries {
			if e.DBID == dbName {
				q := e.Question
				if e.Evidence != "" {
					q = fmt.Sprintf("%s\n\nEvidence (MUST follow these constraints):\n%s", e.Question, e.Evidence)
				}
				questions = append(questions, q)
				goldSQLs = append(goldSQLs, e.SQL)
				if len(questions) >= maxN {
					break
				}
			}
		}
	}

	return questions, goldSQLs
}

// ─────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────

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
		log.Fatalf("Unknown model type: %s", modelType)
		return ""
	}
}
