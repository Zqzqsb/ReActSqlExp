package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reactsql/internal/adapter"
)

// ─────────────────────────────────────────────────────
// Default paths (same as cmd/eval)
// ─────────────────────────────────────────────────────

var defaultDBDirs = map[string]string{
	"spider": "benchmarks/spider/database",
	"bird":   "benchmarks/bird/dev/dev_databases",
}

var defaultSPJPaths = map[string]string{
	"spider": "benchmarks/spider/dev_with_spj.json",
}

// ResultDirInfo holds metadata about a discovered result directory
type ResultDirInfo struct {
	Path      string
	Benchmark string // "spider" or "bird"
	DirName   string // e.g. "20260209_160923_full"
	ModeName  string // e.g. "full" extracted from dirname
	FileCount int    // number of entries in results.json or info.jsonl
	HasJSON   bool   // has results.json
	HasJSONL  bool   // has info.jsonl
}

func main() {
	// Command line flags (for non-interactive usage)
	inputPath := flag.String("input", "", "Input file or directory path (if empty, will auto-discover)")
	outputDir := flag.String("output", "", "Output directory (default: same as input)")
	dbDir := flag.String("db-dir", "", "Database directory (auto-detected if not set)")
	dbType := flag.String("db-type", "", "Database type: sqlite | postgresql (auto-detected if not set)")
	flag.Parse()

	reader := bufio.NewReader(os.Stdin)

	// ── Step 1: Discover or use provided input ──
	var selectedInput string
	var detectedBenchmark string

	if *inputPath != "" {
		// Direct path provided
		selectedInput = *inputPath
		detectedBenchmark = detectBenchmarkFromPath(selectedInput)
	} else {
		// Auto-discover results
		allResults := discoverResults()

		if len(allResults) == 0 {
			fmt.Println("❌ No evaluation results found in results/ directory.")
			fmt.Println("   Run an evaluation first: go run ./cmd/eval")
			os.Exit(1)
		}

		// Interactive selection
		fmt.Println()
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("📊 Select Results to Analyze")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

		currentBenchmark := ""
		for i, r := range allResults {
			if r.Benchmark != currentBenchmark {
				currentBenchmark = r.Benchmark
				fmt.Printf("\n  [%s]\n", strings.ToUpper(currentBenchmark))
			}
			fileType := "json"
			if r.HasJSONL {
				fileType = "jsonl"
			}
			fmt.Printf("  %2d. %-40s (%d examples, %s)\n", i+1, r.DirName, r.FileCount, fileType)
		}

		fmt.Println()
		fmt.Printf("Enter choice [1-%d]: ", len(allResults))
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		var idx int
		if _, err := fmt.Sscanf(input, "%d", &idx); err != nil || idx < 1 || idx > len(allResults) {
			fmt.Printf("❌ Invalid choice: %s\n", input)
			os.Exit(1)
		}

		selected := allResults[idx-1]
		selectedInput = selected.Path
		detectedBenchmark = selected.Benchmark
	}

	// ── Step 2: Auto-detect database directory ──
	resolvedDBDir := *dbDir
	if resolvedDBDir == "" {
		if defaultDir, ok := defaultDBDirs[detectedBenchmark]; ok {
			resolvedDBDir = defaultDir
		} else {
			resolvedDBDir = defaultDBDirs["spider"] // fallback
		}
	}

	// Validate db-dir
	if _, err := os.Stat(resolvedDBDir); os.IsNotExist(err) {
		fmt.Printf("❌ Database directory not found: %s\n", resolvedDBDir)
		fmt.Printf("   Please download the %s benchmark databases first.\n", detectedBenchmark)
		os.Exit(1)
	}

	// ── Step 3: Auto-detect database type ──
	detectedDBType := *dbType
	if detectedDBType == "" {
		dt := DetectDBType(resolvedDBDir)
		if dt == DBTypeUnknown {
			detectedDBType = "sqlite"
		} else {
			detectedDBType = dt.String()
		}
	}

	// ── Step 4: Determine output directory ──
	resolvedOutputDir := *outputDir
	if resolvedOutputDir == "" {
		fileInfo, err := os.Stat(selectedInput)
		if err != nil {
			fmt.Printf("❌ Cannot stat input path: %v\n", err)
			os.Exit(1)
		}
		if fileInfo.IsDir() {
			resolvedOutputDir = selectedInput
		} else {
			resolvedOutputDir = filepath.Dir(selectedInput)
		}
	}

	// ── Step 5: Print config summary ──
	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("🔍 Analyze Results — %s\n", strings.ToUpper(detectedBenchmark))
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Benchmark:      %s\n", detectedBenchmark)
	fmt.Printf("  Input:          %s\n", selectedInput)
	fmt.Printf("  DB Directory:   %s\n", resolvedDBDir)
	fmt.Printf("  DB Type:        %s\n", detectedDBType)
	fmt.Printf("  Output:         %s\n", resolvedOutputDir)
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	// Ensure output directory exists
	if err := EnsureDirectoryExists(resolvedOutputDir); err != nil {
		fmt.Printf("❌ Failed to create output directory: %v\n", err)
		os.Exit(1)
	}

	// ── Step 6: Load input results ──
	ctx := context.Background()
	analyzer := NewSQLAnalyzer()
	reporter := NewReporter(resolvedOutputDir)

	// Determine classified output directory
	var classifiedOutputDir string
	fileInfo, err := os.Stat(selectedInput)
	if err != nil {
		fmt.Printf("❌ Cannot stat input path: %v\n", err)
		os.Exit(1)
	}
	if fileInfo.IsDir() {
		classifiedOutputDir = selectedInput
	} else {
		classifiedOutputDir = filepath.Dir(selectedInput)
	}

	classifier := NewResultClassifier(classifiedOutputDir)

	// Load results
	var inputResults []InputResult
	if fileInfo.IsDir() {
		jsonlPath := filepath.Join(selectedInput, "info.jsonl")
		jsonPath := filepath.Join(selectedInput, "results.json")

		if _, err := os.Stat(jsonlPath); err == nil {
			fmt.Printf("📂 Loading results from: %s\n", jsonlPath)
			inputResults, err = LoadInputFile(jsonlPath)
		} else if _, err2 := os.Stat(jsonPath); err2 == nil {
			fmt.Printf("📂 Loading results from: %s\n", jsonPath)
			inputResults, err = LoadInputFile(jsonPath)
		} else {
			fmt.Printf("❌ No results file found in: %s\n", selectedInput)
			fmt.Println("   Expected: info.jsonl or results.json")
			os.Exit(1)
		}
	} else {
		fmt.Printf("📂 Loading results from: %s\n", selectedInput)
		inputResults, err = LoadInputFile(selectedInput)
	}

	if err != nil {
		fmt.Printf("❌ Failed to load results: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Loaded %d results\n\n", len(inputResults))

	// ── Step 7: Load SPJ tags ──
	if spjPath, ok := defaultSPJPaths[detectedBenchmark]; ok {
		spjTags, err := LoadSPJTags(spjPath)
		if err != nil {
			fmt.Printf("⚠️  Failed to load SPJ tags: %v\n", err)
		} else if len(spjTags) > 0 {
			MergeSPJTags(inputResults, spjTags)
		}
	}

	// ── Step 8: Run analysis ──
	startTime := time.Now()
	var analysisResults []*AnalysisResult

	for i, input := range inputResults {
		if i > 0 && i%50 == 0 {
			fmt.Printf("  ⏳ Processed %d/%d queries...\n", i, len(inputResults))
		}

		// Build database path
		dbPath := input.DBName
		if detectedDBType == "pg" || detectedDBType == "postgres" || detectedDBType == "postgresql" {
			dbPath = "pg:" + input.DBName
		} else {
			dbPath = filepath.Join(resolvedDBDir, input.DBName, input.DBName+".sqlite")
		}

		// Execute SQL queries
		gtResult := &ExecResult{Success: false}
		predResult := &ExecResult{Success: false}
		var gtErr, predErr error

		dbAdapter, err := adapter.NewAdapter(&adapter.DBConfig{
			Type:     "sqlite",
			FilePath: dbPath,
		})
		if err != nil {
			gtResult.Error = fmt.Sprintf("DB connection error: %v", err)
			predResult.Error = fmt.Sprintf("DB connection error: %v", err)
		} else {
			if err := dbAdapter.Connect(ctx); err != nil {
				gtResult.Error = fmt.Sprintf("DB connection error: %v", err)
				predResult.Error = fmt.Sprintf("DB connection error: %v", err)
			} else {
				// Execute gold SQL
				gtData, ge := dbAdapter.ExecuteQuery(ctx, input.GTSQL)
				gtErr = ge
				if ge == nil {
					gtResult.Success = true
					gtResult.Rows = ConvertQueryResultFormat(gtData)
				} else {
					gtResult.Error = ge.Error()
				}

				// Execute predicted SQL
				predData, pe := dbAdapter.ExecuteQuery(ctx, input.PredSQL)
				predErr = pe
				if pe == nil {
					predResult.Success = true
					predResult.Rows = ConvertQueryResultFormat(predData)
				} else {
					predResult.Error = pe.Error()
				}
			}
			dbAdapter.Close()
		}

		// Analyze
		analysisResult := analyzer.AnalyzeSQL(input, gtResult, predResult, gtErr, predErr)
		analysisResults = append(analysisResults, analysisResult)
	}

	elapsedTime := time.Since(startTime)
	stats := analyzer.GetStatistics()

	// ── Step 9: Classify and save ──
	fmt.Printf("\n📁 Classifying analysis results...\n")
	if err := classifier.ClassifyAndSaveResults(analysisResults); err != nil {
		fmt.Printf("⚠️  Failed to classify results: %v\n", err)
	} else {
		fmt.Printf("✅ Classification saved to: %s\n", classifiedOutputDir)
	}

	// ── Step 10: Print summary ──
	reporter.PrintSummary(stats, len(inputResults))

	// Save summary report
	if err := reporter.GenerateSummaryReport(stats, len(inputResults)); err != nil {
		fmt.Printf("⚠️  Failed to save summary report: %v\n", err)
	}

	fmt.Printf("\n⏱️  Analysis completed in %s\n", elapsedTime)
}

// ─────────────────────────────────────────────────────
// Auto-discovery helpers
// ─────────────────────────────────────────────────────

// discoverResults scans results/ directory for evaluation results
func discoverResults() []ResultDirInfo {
	var results []ResultDirInfo

	for _, benchmark := range []string{"spider", "bird"} {
		benchDir := filepath.Join("results", benchmark)
		entries, err := os.ReadDir(benchDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			dirPath := filepath.Join(benchDir, entry.Name())
			info := ResultDirInfo{
				Path:      dirPath,
				Benchmark: benchmark,
				DirName:   entry.Name(),
			}

			// Extract mode name from directory name (e.g. "20260209_160923_full" -> "full")
			parts := strings.SplitN(entry.Name(), "_", 3)
			if len(parts) >= 3 {
				info.ModeName = parts[2]
			}

			// Check for results files and count entries
			jsonlPath := filepath.Join(dirPath, "info.jsonl")
			jsonPath := filepath.Join(dirPath, "results.json")

			if fi, err := os.Stat(jsonlPath); err == nil && fi.Size() > 0 {
				info.HasJSONL = true
				info.FileCount = countJSONLLines(jsonlPath)
			}
			if fi, err := os.Stat(jsonPath); err == nil && fi.Size() > 2 {
				info.HasJSON = true
				if info.FileCount == 0 {
					info.FileCount = countJSONEntries(jsonPath)
				}
			}

			// Only include directories that have some results
			if info.HasJSON || info.HasJSONL {
				results = append(results, info)
			}
		}
	}

	// Sort by benchmark then by dirname (newest first via reverse)
	sort.Slice(results, func(i, j int) bool {
		if results[i].Benchmark != results[j].Benchmark {
			return results[i].Benchmark < results[j].Benchmark
		}
		return results[i].DirName > results[j].DirName // newest first
	})

	return results
}

// detectBenchmarkFromPath guesses benchmark type from path
func detectBenchmarkFromPath(path string) string {
	pathLower := strings.ToLower(path)
	if strings.Contains(pathLower, "bird") {
		return "bird"
	}
	return "spider"
}

// countJSONLLines counts non-empty lines in a JSONL file
func countJSONLLines(path string) int {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	return count
}

// countJSONEntries counts entries in a JSON array file (lightweight, no full parse)
func countJSONEntries(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	// Count "db_id" occurrences as a proxy for number of entries
	return strings.Count(string(data), `"db_id"`)
}
