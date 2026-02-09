package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"reactsql/internal/adapter"
	"reactsql/internal/agent"
	contextpkg "reactsql/internal/context"
	"reactsql/internal/llm"
	"reactsql/internal/logger"
)

// devQueryEntry represents one entry in the Spider dev JSON file
type devQueryEntry struct {
	DBID string `json:"db_id"`
}

// Default paths
var defaultGenPaths = map[string]map[string]string{
	"spider": {
		"dev-file":   "benchmarks/spider_corrected/dev_with_fields.json",
		"db-dir":     "benchmarks/spider/database",
		"output-dir": "contexts/sqlite/spider",
	},
	"bird": {
		"db-dir":     "benchmarks/bird/dev/dev_databases",
		"output-dir": "contexts/sqlite/bird",
	},
}

func main() {
	benchmark := flag.String("benchmark", "", "Benchmark: spider | bird (if empty, will ask interactively)")
	modelType := flag.String("model", "deepseek-v3", "Model: deepseek-v3 | deepseek-v3.2 | qwen-max | qwen3-max | ali-deepseek-v3.2")
	workers := flag.Int("workers", 2, "Number of concurrent workers")
	skipExisting := flag.Bool("skip-existing", true, "Skip databases that already have Rich Context")
	devFile := flag.String("dev-file", "", "Spider dev dataset JSON file path (auto-detected)")
	dbDir := flag.String("db-dir", "", "Database directory (auto-detected)")
	outputDir := flag.String("output-dir", "", "Output directory (auto-detected)")
	flag.Parse()

	reader := bufio.NewReader(os.Stdin)

	// ── Step 1: Select benchmark ──
	if *benchmark == "" {
		fmt.Println()
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("🧠 Rich Context Generator")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("  Generate Rich Context (table/column descriptions)")
		fmt.Println("  for all databases in a benchmark dev set.")
		fmt.Println()
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("📦 Select Benchmark")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

		// Show existing context status
		for i, bm := range []string{"spider", "bird"} {
			existingCount := countExistingContexts(defaultGenPaths[bm]["output-dir"])
			status := ""
			if existingCount > 0 {
				status = fmt.Sprintf(" (%d contexts already generated)", existingCount)
			}
			desc := "Spider dev set — cross-database"
			if bm == "bird" {
				desc = "BIRD dev set — with evidence hints"
			}
			fmt.Printf("  %d. %-8s — %s%s\n", i+1, bm, desc, status)
		}
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

	// ── Step 2: Resolve paths ──
	paths := defaultGenPaths[*benchmark]
	if *devFile == "" {
		*devFile = paths["dev-file"]
	}
	resolvedDBDir := resolveDir(*dbDir, paths["db-dir"])
	resolvedOutputDir := resolveDir(*outputDir, paths["output-dir"])

	// Validate paths
	if _, err := os.Stat(resolvedDBDir); os.IsNotExist(err) {
		log.Fatalf("❌ Database directory not found: %s\n   Please download the %s benchmark databases first.", resolvedDBDir, *benchmark)
	}

	model := parseModelType(*modelType)

	switch *benchmark {
	case "spider":
		runSpider(model, *devFile, resolvedDBDir, resolvedOutputDir, *workers, *skipExisting)
	case "bird":
		runBird(model, resolvedDBDir, resolvedOutputDir, *workers, *skipExisting)
	}
}

// countExistingContexts counts .json files in a directory
func countExistingContexts(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			count++
		}
	}
	return count
}

// resolveDir returns override if non-empty, otherwise returns defaultDir
func resolveDir(override, defaultDir string) string {
	if override != "" {
		return override
	}
	return defaultDir
}

// parseModelType converts model type string to llm.ModelType
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

// ─────────────────────────────────────────────────────
// Spider: reads db_ids from dev_with_fields.json
// ─────────────────────────────────────────────────────

func runSpider(model llm.ModelType, devFile, dbDir, outputDir string, workerCount int, skipExisting bool) {
	existingCount := countExistingContexts(outputDir)
	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("🚀 Spider — Rich Context Generator")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Dev file:      %s\n", devFile)
	fmt.Printf("  DB dir:        %s\n", dbDir)
	fmt.Printf("  Output dir:    %s\n", outputDir)
	fmt.Printf("  Workers:       %d\n", workerCount)
	fmt.Printf("  Skip existing: %v\n", skipExisting)
	fmt.Printf("  Model:         %s\n", llm.GetModelDisplayName(model))
	if existingCount > 0 {
		fmt.Printf("  Existing:      %d contexts already generated\n", existingCount)
	}
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	// 1. Extract unique db_ids from dev file
	databases, err := extractSpiderDevDBIDs(devFile)
	if err != nil {
		log.Fatalf("Failed to read dev file: %v", err)
	}
	fmt.Printf("Found %d databases in Spider dev set\n\n", len(databases))

	databases = filterExisting(databases, outputDir, skipExisting)
	runBatch(model, databases, dbDir, outputDir, workerCount, true)
}

// extractSpiderDevDBIDs reads the dev JSON file and returns sorted unique db_ids
func extractSpiderDevDBIDs(devFile string) ([]string, error) {
	data, err := os.ReadFile(devFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", devFile, err)
	}

	var entries []devQueryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", devFile, err)
	}

	seen := make(map[string]bool)
	var databases []string
	for _, entry := range entries {
		if !seen[entry.DBID] {
			seen[entry.DBID] = true
			databases = append(databases, entry.DBID)
		}
	}

	sort.Strings(databases)
	return databases, nil
}

// ─────────────────────────────────────────────────────
// BIRD: scans database directory
// ─────────────────────────────────────────────────────

func runBird(model llm.ModelType, dbDir, outputDir string, workerCount int, skipExisting bool) {
	existingCount := countExistingContexts(outputDir)
	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("🚀 BIRD — Rich Context Generator")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  DB dir:        %s\n", dbDir)
	fmt.Printf("  Output dir:    %s\n", outputDir)
	fmt.Printf("  Workers:       %d\n", workerCount)
	fmt.Printf("  Skip existing: %v\n", skipExisting)
	fmt.Printf("  Model:         %s\n", llm.GetModelDisplayName(model))
	if existingCount > 0 {
		fmt.Printf("  Existing:      %d contexts already generated\n", existingCount)
	}
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	// Scan database directory for sub-directories
	entries, err := os.ReadDir(dbDir)
	if err != nil {
		log.Fatalf("Failed to read database directory: %v", err)
	}

	var databases []string
	for _, entry := range entries {
		if entry.IsDir() {
			databases = append(databases, entry.Name())
		}
	}
	sort.Strings(databases)
	fmt.Printf("Found %d databases in BIRD dev set\n\n", len(databases))

	databases = filterExisting(databases, outputDir, skipExisting)
	runBatch(model, databases, dbDir, outputDir, workerCount, false)
}

// ─────────────────────────────────────────────────────
// Common batch runner
// ─────────────────────────────────────────────────────

func filterExisting(databases []string, outputDir string, skipExisting bool) []string {
	if !skipExisting {
		return databases
	}
	var toProcess []string
	for _, db := range databases {
		outputFile := filepath.Join(outputDir, db+".json")
		if _, err := os.Stat(outputFile); os.IsNotExist(err) {
			toProcess = append(toProcess, db)
		} else {
			fmt.Printf("⏭️  Skip %s (already exists)\n", db)
		}
	}
	fmt.Printf("\nNeed to process %d databases\n\n", len(toProcess))
	return toProcess
}

func runBatch(model llm.ModelType, databases []string, dbDir, outputDir string, workerCount int, loadSchema bool) {
	if len(databases) == 0 {
		fmt.Println("All databases already have Rich Context. Nothing to do.")
		return
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output dir: %v", err)
	}

	// Create multi-progress display
	mp := logger.NewMultiProgress(
		fmt.Sprintf("🚀 Processing %d databases (workers: %d)", len(databases), workerCount),
		databases,
	)
	mp.Start()

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, workerCount)

	for _, dbName := range databases {
		wg.Add(1)
		semaphore <- struct{}{}

		go func(name string) {
			defer wg.Done()
			defer func() { <-semaphore }()

			mp.StartTask(name)

			if err := processDatabase(model, dbDir, outputDir, name, loadSchema, mp); err != nil {
				mp.FailTask(name, err)
			} else {
				mp.CompleteTask(name)
			}
		}(dbName)
	}

	wg.Wait()
	mp.Stop()

	// Print summary
	fmt.Print(mp.Summary())
	fmt.Printf("✅ Rich Context files saved to: %s\n", outputDir)
}

// ─────────────────────────────────────────────────────
// Single database processing (shared by spider & bird)
// ─────────────────────────────────────────────────────

func processDatabase(model llm.ModelType, dbDir, outputDir, dbName string, loadSchema bool, mp *logger.MultiProgress) error {
	ctx := context.Background()

	// Helper to update progress display
	update := func(phase string, progress int) {
		if mp != nil {
			mp.UpdateTask(dbName, phase, progress)
		}
	}

	update("Connecting...", 0)

	// 1. Create adapter
	dbPath := filepath.Join(dbDir, dbName, dbName+".sqlite")
	dbAdapter, err := adapter.NewAdapter(&adapter.DBConfig{
		Type:     "sqlite",
		FilePath: dbPath,
	})
	if err != nil {
		return fmt.Errorf("failed to create adapter: %w", err)
	}

	if err := dbAdapter.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer dbAdapter.Close()

	// 2. Create SharedContext (quiet mode for multi-progress)
	sharedCtx := contextpkg.NewSharedContext(dbName, "sqlite")
	if mp != nil {
		sharedCtx.Quiet = true
	}

	// 2.1 Load schema.sql if available
	if loadSchema {
		schemaPath := filepath.Join(dbDir, dbName, "schema.sql")
		if _, err := os.Stat(schemaPath); err == nil {
			if err := sharedCtx.LoadSchemaFromFile(schemaPath); err != nil && !sharedCtx.Quiet {
				fmt.Printf("[%s] ⚠️  Warning: failed to load schema.sql: %v\n", dbName, err)
			}
		}
	}

	// 3. Create LLM
	update("Creating LLM...", 5)
	llmInstance, err := llm.CreateLLMByType(model)
	if err != nil {
		return fmt.Errorf("failed to create LLM: %w", err)
	}

	// 4. Phase 1: Coordinator Agent discovers tables
	update("Phase 1: Discovering tables", 10)

	var progLogger *logger.Logger
	if !sharedCtx.Quiet {
		progLogger = logger.NewLogger(0)
		progLogger.SetPhase(fmt.Sprintf("[%s] Phase 1: Discovering Tables", dbName))
	}

	coordinator, err := agent.NewCoordinatorAgent("coordinator", llmInstance, dbAdapter, sharedCtx)
	if err != nil {
		return fmt.Errorf("failed to create coordinator: %w", err)
	}

	if err := coordinator.Execute(ctx); err != nil {
		return fmt.Errorf("coordinator failed: %w", err)
	}

	// 5. Phase 2: Worker Agents analyze tables in parallel
	tasks := sharedCtx.GetAllTasks()
	var workerTasks []*contextpkg.TaskInfo
	for _, task := range tasks {
		if task.AgentID != "coordinator" {
			workerTasks = append(workerTasks, task)
		}
	}

	totalWorkers := len(workerTasks)
	update(fmt.Sprintf("Phase 2: Analyzing %d tables", totalWorkers), 20)

	if !sharedCtx.Quiet {
		progLogger = logger.NewLogger(totalWorkers)
		progLogger.SetPhase(fmt.Sprintf("[%s] Phase 2: Analyzing %d Tables", dbName, totalWorkers))
	}

	var wg sync.WaitGroup
	var completedWorkers int32 = 0
	var workerMu sync.Mutex

	for _, task := range workerTasks {
		tableName := task.ID[8:] // strip "analyze_" prefix

		wg.Add(1)
		go func(taskID, agentID, tblName string) {
			defer wg.Done()

			if !sharedCtx.Quiet {
				progLogger.StartTask(tblName)
			}

			worker, err := agent.NewWorkerAgent(agentID, taskID, tblName, llmInstance, dbAdapter, sharedCtx)
			if err != nil {
				if !sharedCtx.Quiet {
					progLogger.FailTask(tblName, err)
				}
				return
			}

			if err := worker.Execute(ctx); err != nil {
				if !sharedCtx.Quiet {
					progLogger.FailTask(tblName, err)
				}
			} else {
				if !sharedCtx.Quiet {
					progLogger.CompleteTask(tblName)
				}
			}

			// Update multi-progress: map worker completion to 20%..90% range
			workerMu.Lock()
			completedWorkers++
			prog := 20 + int(float64(completedWorkers)/float64(totalWorkers)*70)
			workerMu.Unlock()
			update(fmt.Sprintf("Phase 2: %d/%d tables done", completedWorkers, totalWorkers), prog)
		}(task.ID, task.AgentID, tableName)
	}

	wg.Wait()

	// 6. Analyze JOIN paths
	update("Analyzing JOIN paths", 92)
	sharedCtx.AnalyzeJoinPaths()
	if !sharedCtx.Quiet {
		progLogger.PrintSummary()
	}

	// 7. Save to file
	update("Saving context file", 95)
	os.MkdirAll(outputDir, 0755)
	outputFile := filepath.Join(outputDir, dbName+".json")
	if err := sharedCtx.SaveToFile(outputFile); err != nil {
		return fmt.Errorf("failed to save: %w", err)
	}

	update("Done", 100)
	return nil
}
