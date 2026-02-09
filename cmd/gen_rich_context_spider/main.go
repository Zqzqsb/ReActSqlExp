package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"reactsql/internal/adapter"
	"reactsql/internal/agent"
	contextpkg "reactsql/internal/context"
	"reactsql/internal/llm"
	"reactsql/internal/logger"
)

// DBConfigFile represents a database configuration file
type DBConfigFile struct {
	Type        string `json:"type"`
	Host        string `json:"host,omitempty"`
	Port        int    `json:"port,omitempty"`
	Database    string `json:"database,omitempty"`
	User        string `json:"user,omitempty"`
	Password    string `json:"password,omitempty"`
	FilePath    string `json:"file_path,omitempty"`
	SSLMode     string `json:"ssl_mode,omitempty"`
	Description string `json:"description,omitempty"`
}

func main() {
	configPath := flag.String("config", "", "Database config file path (e.g. dbs/spider/concert_singer.json)")
	modelType := flag.String("model", "deepseek-v3", "Model type: deepseek-v3, deepseek-v3.2, qwen-max, qwen3-max, ali-deepseek-v3.2")
	outputDir := flag.String("output-dir", "contexts/sqlite/spider", "Rich Context output directory")
	flag.Parse()

	model := parseModelType(*modelType)

	if *configPath == "" {
		fmt.Println("Usage:")
		fmt.Println("  Single DB:  go run ./cmd/gen_rich_context_spider --config dbs/spider/concert_singer.json")
		fmt.Println("  All dev:    go run ./cmd/gen_all_dev --benchmark spider")
		fmt.Println()
		flag.PrintDefaults()
		os.Exit(1)
	}

	fmt.Println("🚀 Multi-Agent Database Analysis System")
	fmt.Printf("📁 Config: %s\n", *configPath)
	fmt.Printf("🤖 Model: %s\n\n", llm.GetModelDisplayName(model))

	configFile, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Determine output directory from config path
	effectiveOutputDir := *outputDir
	configDirName := filepath.Dir(*configPath)
	if filepath.Base(configDirName) == "spider" {
		effectiveOutputDir = filepath.Join("contexts", configFile.Type, "spider")
	} else {
		effectiveOutputDir = filepath.Join("contexts", configFile.Type)
	}

	if err := processSingleDatabase(model, configFile, effectiveOutputDir); err != nil {
		log.Fatalf("Failed: %v", err)
	}

	fmt.Println("\n✅ Multi-Agent Analysis Complete!")
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

// processSingleDatabase processes a single database: connect, analyze, save Rich Context
func processSingleDatabase(model llm.ModelType, configFile *DBConfigFile, outputDir string) error {
	if configFile.Description != "" {
		fmt.Printf("📝 %s\n", configFile.Description)
	}

	dbAdapter, err := createAdapter(configFile)
	if err != nil {
		return fmt.Errorf("failed to create adapter: %w", err)
	}

	ctx := context.Background()

	if err := dbAdapter.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer dbAdapter.Close()

	version, _ := dbAdapter.GetDatabaseVersion(ctx)
	fmt.Printf("✓ Connected to %s database: %s (version: %s)\n",
		dbAdapter.GetDatabaseType(), configFile.Database, version)

	sharedCtx := contextpkg.NewSharedContext(configFile.Database, dbAdapter.GetDatabaseType())

	// Load schema.sql if available
	if configFile.Type == "sqlite" && configFile.FilePath != "" {
		dbDirPath := filepath.Dir(configFile.FilePath)
		schemaPath := filepath.Join(dbDirPath, "schema.sql")

		if _, err := os.Stat(schemaPath); err == nil {
			fmt.Printf("📄 Loading schema from: %s\n", schemaPath)
			if err := sharedCtx.LoadSchemaFromFile(schemaPath); err != nil {
				fmt.Printf("⚠️  Warning: Failed to load schema.sql: %v\n", err)
			} else {
				fmt.Println("✓ Schema loaded with foreign key relationships")
			}
		}
	}

	llmInstance, err := llm.CreateLLMByType(model)
	if err != nil {
		return fmt.Errorf("failed to create LLM: %w", err)
	}

	startTime := time.Now()

	// Phase 1: Coordinator Agent discovers tables
	progLogger := logger.NewLogger(0)
	progLogger.SetPhase(fmt.Sprintf("[%s] Phase 1: Coordinator Agent - Discovering Tables", configFile.Database))

	coordinator, err := agent.NewCoordinatorAgent("coordinator", llmInstance, dbAdapter, sharedCtx)
	if err != nil {
		return fmt.Errorf("failed to create coordinator: %w", err)
	}

	if err := coordinator.Execute(ctx); err != nil {
		return fmt.Errorf("coordinator failed: %w", err)
	}

	// Phase 2: Worker Agents analyze tables in parallel
	tasks := sharedCtx.GetAllTasks()
	var workerTasks []*contextpkg.TaskInfo
	for _, task := range tasks {
		if task.AgentID != "coordinator" {
			workerTasks = append(workerTasks, task)
		}
	}

	progLogger = logger.NewLogger(len(workerTasks))
	progLogger.SetPhase(fmt.Sprintf("[%s] Phase 2: Worker Agents - Analyzing %d Tables", configFile.Database, len(workerTasks)))

	var wg sync.WaitGroup
	for _, task := range workerTasks {
		tableName := task.ID[8:] // strip "analyze_" prefix

		wg.Add(1)
		go func(taskID, agentID, tblName string) {
			defer wg.Done()

			progLogger.StartTask(tblName)

			worker, err := agent.NewWorkerAgent(agentID, taskID, tblName, llmInstance, dbAdapter, sharedCtx)
			if err != nil {
				progLogger.FailTask(tblName, err)
				return
			}

			if err := worker.Execute(ctx); err != nil {
				progLogger.FailTask(tblName, err)
				return
			}

			progLogger.CompleteTask(tblName)
		}(task.ID, task.AgentID, tableName)
	}

	wg.Wait()

	sharedCtx.AnalyzeJoinPaths()

	duration := time.Since(startTime)
	progLogger.PrintSummary()

	fmt.Printf("[%s] Analysis complete in %v\n", configFile.Database, duration)

	os.MkdirAll(outputDir, 0755)
	contextFile := filepath.Join(outputDir, configFile.Database+".json")

	if err := sharedCtx.SaveToFile(contextFile); err != nil {
		return fmt.Errorf("failed to save: %w", err)
	}
	fmt.Printf("✓ Results saved to: %s\n", contextFile)

	return nil
}

// loadConfig loads a database config JSON file
func loadConfig(path string) (*DBConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config DBConfigFile
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

// createAdapter creates a database adapter from config
func createAdapter(config *DBConfigFile) (adapter.DBAdapter, error) {
	dbConfig := &adapter.DBConfig{
		Type:     config.Type,
		Host:     config.Host,
		Port:     config.Port,
		Database: config.Database,
		User:     config.User,
		Password: config.Password,
		FilePath: config.FilePath,
	}

	return adapter.NewAdapter(dbConfig)
}
