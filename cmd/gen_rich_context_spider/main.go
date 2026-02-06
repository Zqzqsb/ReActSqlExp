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

// DBConfigFile 数据库配置文件结构
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
	// 解析命令行参数
	configPath := flag.String("config", "", "数据库配置文件路径 (例如: dbs/mysql/testdb.json)")
	modelType := flag.String("model", "deepseek-v3", "模型类型: deepseek-v3, deepseek-v3.2, qwen-max, qwen3-max, ali-deepseek-v3.2")
	flag.Parse()

	if *configPath == "" {
		log.Fatal("请指定配置文件: --config dbs/mysql/testdb.json")
	}

	// 解析模型类型
	var model llm.ModelType
	switch *modelType {
	case "deepseek-v3":
		model = llm.ModelDeepSeekV3
	case "deepseek-v3.2":
		model = llm.ModelDeepSeekV32
	case "qwen-max":
		model = llm.ModelQwenMax
	case "qwen3-max":
		model = llm.ModelQwen3Max
	case "ali-deepseek-v3.2":
		model = llm.ModelAliDeepSeekV32
	default:
		log.Fatalf("Unknown model type: %s. Available: deepseek-v3, deepseek-v3.2, qwen-max, qwen3-max, ali-deepseek-v3.2", *modelType)
	}

	fmt.Println("🚀 Multi-Agent Database Analysis System")
	fmt.Printf("📁 Config: %s\n", *configPath)
	fmt.Printf("🤖 Model: %s\n\n", llm.GetModelDisplayName(model))

	// 1. 加载配置文件
	configFile, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if configFile.Description != "" {
		fmt.Printf("📝 %s\n\n", configFile.Description)
	}

	// 2. 创建数据库适配器
	dbAdapter, err := createAdapter(configFile)
	if err != nil {
		log.Fatalf("Failed to create adapter: %v", err)
	}

	ctx := context.Background()

	if err := dbAdapter.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer dbAdapter.Close()

	// 获取数据库版本
	version, _ := dbAdapter.GetDatabaseVersion(ctx)
	fmt.Printf("✓ Connected to %s database: %s (version: %s)\n\n",
		dbAdapter.GetDatabaseType(), configFile.Database, version)

	// 3. 创建SharedContext
	sharedCtx := contextpkg.NewSharedContext(configFile.Database, dbAdapter.GetDatabaseType())
	fmt.Println("✓ SharedContext created")

	// 3.1 加载 schema.sql（如果存在）
	if configFile.Type == "sqlite" && configFile.FilePath != "" {
		// 从 FilePath 推导 schema.sql 路径
		// 例如: benchmarks/spider/database/academic/academic.sqlite -> benchmarks/spider/database/academic/schema.sql
		dbDir := filepath.Dir(configFile.FilePath)
		schemaPath := filepath.Join(dbDir, "schema.sql")

		if _, err := os.Stat(schemaPath); err == nil {
			fmt.Printf("📄 Loading schema from: %s\n", schemaPath)
			if err := sharedCtx.LoadSchemaFromFile(schemaPath); err != nil {
				fmt.Printf("⚠️  Warning: Failed to load schema.sql: %v\n", err)
			} else {
				fmt.Println("✓ Schema loaded with foreign key relationships")
			}
		} else {
			fmt.Printf("⚠️  Warning: schema.sql not found at %s\n", schemaPath)
		}
	}
	fmt.Println()

	// 4. 创建LLM
	llmInstance, err := llm.CreateLLMByType(model)
	if err != nil {
		log.Fatal(err)
	}

	startTime := time.Now()

	// 5. Phase 1: 调度Agent发现表
	progLogger := logger.NewLogger(0) // 初始不知道总任务数
	progLogger.SetPhase("Phase 1: Coordinator Agent - Discovering Tables")

	coordinator, err := agent.NewCoordinatorAgent("coordinator", llmInstance, dbAdapter, sharedCtx)
	if err != nil {
		log.Fatal(err)
	}

	if err := coordinator.Execute(ctx); err != nil {
		log.Fatalf("Coordinator failed: %v", err)
	}

	// 6. Phase 2: 工作Agent并行分析表
	tasks := sharedCtx.GetAllTasks()
	var workerTasks []*contextpkg.TaskInfo
	for _, task := range tasks {
		if task.AgentID != "coordinator" {
			workerTasks = append(workerTasks, task)
		}
	}

	// 更新日志器的总任务数
	progLogger = logger.NewLogger(len(workerTasks))
	progLogger.SetPhase(fmt.Sprintf("Phase 2: Worker Agents - Analyzing %d Tables", len(workerTasks)))

	var wg sync.WaitGroup

	for _, task := range workerTasks {
		// 从任务ID提取表名 (analyze_tablename)
		tableName := task.ID[8:] // 去掉 "analyze_" 前缀

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

	// 等待所有工作Agent完成
	wg.Wait()

	// 6.5. 分析 JOIN 路径和字段语义
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("🔗 Analyzing JOIN Paths and Field Semantics")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	sharedCtx.AnalyzeJoinPaths()
	fmt.Printf("✓ Analyzed %d join paths\n", len(sharedCtx.JoinPaths))
	fmt.Printf("✓ Analyzed %d field semantics\n", len(sharedCtx.FieldSemantics))

	duration := time.Since(startTime)
	progLogger.PrintSummary()

	// 7. 显示结果
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("📊 Analysis Complete")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("\nTotal Time: %v\n\n", duration)

	fmt.Println(sharedCtx.GetSummary())

	// 8. 显示收集的数据
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("💾 Collected Data")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	allData := sharedCtx.GetAllData()
	for key, value := range allData {
		fmt.Printf("\n%s:\n", key)
		fmt.Printf("  %v\n", value)
	}

	// 9. 保存到文件
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("💾 Saving Results")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// 生成输出文件名：根据配置路径决定输出路径
	// dbs/spider/*.json -> contexts/sqlite/spider/*.json
	// dbs/mysql/*.json -> contexts/mysql/*.json
	var outputDir string
	configDir := filepath.Dir(*configPath)
	if filepath.Base(configDir) == "spider" {
		outputDir = filepath.Join("contexts", configFile.Type, "spider")
	} else {
		outputDir = filepath.Join("contexts", configFile.Type)
	}

	os.MkdirAll(outputDir, 0755)
	contextFile := filepath.Join(outputDir, configFile.Database+".json")

	if err := sharedCtx.SaveToFile(contextFile); err != nil {
		log.Printf("Failed to save: %v\n", err)
	} else {
		fmt.Printf("✓ Results saved to: %s\n", contextFile)
	}

	fmt.Println("\n✅ Multi-Agent Analysis Complete!")
}

// loadConfig 加载数据库配置文件
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

// createAdapter 根据配置创建数据库适配器
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
