package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tmc/langchaingo/llms/openai"

	"reactsql/internal/adapter"
	"reactsql/internal/agent"
	contextpkg "reactsql/internal/context"
	"reactsql/internal/logger"
)

func main() {
	// 解析命令行参数
	dbDir := flag.String("db-dir", "benchmarks/bird/dev/dev_databases", "BIRD数据库目录")
	outputDir := flag.String("output-dir", "contexts/sqlite/bird", "输出目录")
	dbName := flag.String("db", "", "指定单个数据库名称（为空则处理全部）")
	workers := flag.Int("workers", 3, "并发处理的数据库数量")
	skipExisting := flag.Bool("skip-existing", true, "跳过已存在的Rich Context文件")
	useV32 := flag.Bool("v3.2", false, "使用 DeepSeek-V3.2 模型（默认使用 V3）")

	flag.Parse()

	// 根据标志选择模型
	modelName := "deepseek-v3-250324"
	modelDisplay := "DeepSeek-V3"
	if *useV32 {
		modelName = "deepseek-v3-2-251201"
		modelDisplay = "DeepSeek-V3.2"
	}

	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("🚀 BIRD Rich Context Generator")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("数据库目录: %s\n", *dbDir)
	fmt.Printf("输出目录: %s\n", *outputDir)
	fmt.Printf("并发数: %d\n", *workers)
	fmt.Printf("🤖 Model: %s\n", modelDisplay)
	fmt.Println()

	// 创建输出目录
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("创建输出目录失败: %v", err)
	}

	// 获取所有数据库
	var databases []string
	if *dbName != "" {
		// 处理单个数据库
		databases = []string{*dbName}
	} else {
		// 获取所有数据库
		entries, err := os.ReadDir(*dbDir)
		if err != nil {
			log.Fatalf("读取数据库目录失败: %v", err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				databases = append(databases, entry.Name())
			}
		}
	}

	fmt.Printf("找到 %d 个数据库\n\n", len(databases))

	// 过滤已存在的
	if *skipExisting {
		var toProcess []string
		for _, db := range databases {
			outputFile := filepath.Join(*outputDir, db+".json")
			if _, err := os.Stat(outputFile); os.IsNotExist(err) {
				toProcess = append(toProcess, db)
			} else {
				fmt.Printf("⏭️  跳过 %s (已存在)\n", db)
			}
		}
		databases = toProcess
		fmt.Printf("\n需要处理 %d 个数据库\n\n", len(databases))
	}

	if len(databases) == 0 {
		fmt.Println("没有需要处理的数据库")
		return
	}

	// 创建LLM
	llm, err := openai.New(
		openai.WithModel(modelName),
		openai.WithToken("404b0d95-e938-4fbb-8724-34d2f0dadb00"),
		openai.WithBaseURL("https://ark.cn-beijing.volces.com/api/v3"),
	)
	if err != nil {
		log.Fatalf("初始化LLM失败: %v", err)
	}

	// 并发处理
	startTime := time.Now()
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, *workers)
	successCount := 0
	failCount := 0
	var mu sync.Mutex

	for i, dbName := range databases {
		wg.Add(1)
		semaphore <- struct{}{} // 获取信号量

		go func(idx int, name string) {
			defer wg.Done()
			defer func() { <-semaphore }() // 释放信号量

			fmt.Printf("[%d/%d] 🔄 处理 %s ...\n", idx+1, len(databases), name)

			if err := processDatabase(llm, *dbDir, *outputDir, name); err != nil {
				fmt.Printf("[%d/%d] ❌ 失败 %s: %v\n", idx+1, len(databases), name, err)
				mu.Lock()
				failCount++
				mu.Unlock()
			} else {
				fmt.Printf("[%d/%d] ✅ 完成 %s\n", idx+1, len(databases), name)
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i, dbName)
	}

	wg.Wait()
	duration := time.Since(startTime)

	// 打印摘要
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("📊 处理完成")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("总数: %d\n", len(databases))
	fmt.Printf("成功: %d\n", successCount)
	fmt.Printf("失败: %d\n", failCount)
	fmt.Printf("耗时: %v\n", duration)
	fmt.Printf("平均: %.2fs/数据库\n", duration.Seconds()/float64(len(databases)))
	fmt.Println()
	fmt.Printf("✅ Rich Context 文件保存在: %s\n", *outputDir)
}

func processDatabase(llm *openai.LLM, dbDir, outputDir, dbName string) error {
	ctx := context.Background()

	// 1. 创建数据库适配器
	dbPath := filepath.Join(dbDir, dbName, dbName+".sqlite")
	dbAdapter, err := adapter.NewAdapter(&adapter.DBConfig{
		Type:     "sqlite",
		FilePath: dbPath,
	})
	if err != nil {
		return fmt.Errorf("创建adapter失败: %w", err)
	}

	if err := dbAdapter.Connect(ctx); err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}
	defer dbAdapter.Close()

	// 2. 创建SharedContext
	sharedCtx := contextpkg.NewSharedContext(dbName, "sqlite")

	// 3. Phase 1: Coordinator Agent发现表
	progLogger := logger.NewLogger(0)
	progLogger.SetPhase(fmt.Sprintf("[%s] Phase 1: Discovering Tables", dbName))

	coordinator, err := agent.NewCoordinatorAgent("coordinator", llm, dbAdapter, sharedCtx)
	if err != nil {
		return fmt.Errorf("创建coordinator失败: %w", err)
	}

	if err := coordinator.Execute(ctx); err != nil {
		return fmt.Errorf("coordinator执行失败: %w", err)
	}

	// 4. Phase 2: Worker Agents分析表
	tasks := sharedCtx.GetAllTasks()
	var workerTasks []*contextpkg.TaskInfo
	for _, task := range tasks {
		if task.AgentID != "coordinator" {
			workerTasks = append(workerTasks, task)
		}
	}

	progLogger = logger.NewLogger(len(workerTasks))
	progLogger.SetPhase(fmt.Sprintf("[%s] Phase 2: Analyzing %d Tables", dbName, len(workerTasks)))

	var wg sync.WaitGroup
	for _, task := range workerTasks {
		tableName := task.ID[8:] // 去掉 "analyze_" 前缀

		wg.Add(1)
		go func(taskID, agentID, tblName string) {
			defer wg.Done()

			progLogger.StartTask(tblName)

			worker, err := agent.NewWorkerAgent(agentID, taskID, tblName, llm, dbAdapter, sharedCtx)
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

	// 5. 保存结果
	outputFile := filepath.Join(outputDir, dbName+".json")
	if err := sharedCtx.SaveToFile(outputFile); err != nil {
		return fmt.Errorf("保存文件失败: %w", err)
	}

	return nil
}
