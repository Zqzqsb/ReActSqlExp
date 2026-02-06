package main

import (
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

	"github.com/tmc/langchaingo/llms"
	"reactsql/internal/adapter"
	"reactsql/internal/inference"
	"reactsql/internal/llm"
)

// SpiderExample Spider 数据集中的一个样例
type SpiderExample struct {
	DbID                    string   `json:"db_id"`
	Query                   string   `json:"query"`
	Question                string   `json:"question"`
	ResultFields            []string `json:"result_fields"`
	ResultFieldsDescription string   `json:"result_fields_description"`
}

func main() {
	// 命令行参数
	devJSON := flag.String("dev", "benchmarks/spider_corrected/dev_with_field_with_id.json", "dev.json 路径")
	dbDir := flag.String("db-dir", "benchmarks/spider/database", "数据库目录")
	contextDir := flag.String("context-dir", "contexts/sqlite/spider", "Rich Context 目录")
	outputDir := flag.String("output-dir", "", "结果输出目录（为空则自动生成时间戳目录）")
	modelType := flag.String("model", "deepseek-v3", "模型类型: deepseek-v3 | deepseek-v3.2 | qwen-max | qwen3-max | ali-deepseek-v3.2")

	// 消融实验配置
	useRichContext := flag.Bool("use-rich-context", false, "使用 Rich Context")
	useReact := flag.Bool("use-react", false, "使用 ReAct 循环")
	reactLinking := flag.Bool("react-linking", false, "Schema Linking 使用 ReAct 模式")
	enableClarify := flag.String("enable-clarify", "off", "字段澄清模式: off (不启用) | on (agent主动询问) | force (强制在prompt中给出)")
	enableProofread := flag.Bool("enable-proofread", false, "启用校对模式（允许 LLM 修正 Rich Context）")
	logMode := flag.String("log-mode", "simple", "日志模式: simple (简洁模式) | full (完整输出所有交互)")

	// 测试范围
	startIdx := flag.Int("start", 0, "起始索引")
	endIdx := flag.Int("end", -1, "结束索引（-1 表示全部）")

	flag.Parse()

	// 解析模型类型
	var modelTypeEnum llm.ModelType
	switch *modelType {
	case "deepseek-v3":
		modelTypeEnum = llm.ModelDeepSeekV3
	case "deepseek-v3.2":
		modelTypeEnum = llm.ModelDeepSeekV32
	case "qwen-max":
		modelTypeEnum = llm.ModelQwenMax
	case "qwen3-max":
		modelTypeEnum = llm.ModelQwen3Max
	case "ali-deepseek-v3.2":
		modelTypeEnum = llm.ModelAliDeepSeekV32
	default:
		log.Fatalf("Unknown model type: %s", *modelType)
	}

	modelDisplayName := llm.GetModelDisplayName(modelTypeEnum)

	// 创建输出目录（带时间戳）
	if *outputDir == "" {
		timestamp := time.Now().Format("20060102_150405")
		mode := "baseline"
		if *useRichContext && *useReact {
			mode = "full"
		} else if *useRichContext {
			mode = "rich_context"
		} else if *useReact {
			mode = "react"
		}

		// 添加校对模式后缀
		if *enableProofread {
			mode = mode + "_with_proofread"
		}

		// 添加澄清模式后缀
		if *enableClarify != "off" {
			mode = mode + "_clarify_" + *enableClarify
		}

		*outputDir = filepath.Join("results/spider", fmt.Sprintf("%s_%s", timestamp, mode))
	}

	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("🚀 Spider Dataset Evaluation")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("Config:\n")
	fmt.Printf("  Model: %s\n", modelDisplayName)
	fmt.Printf("  Use Rich Context: %v\n", *useRichContext)
	fmt.Printf("  Use ReAct: %v\n", *useReact)
	fmt.Printf("  React Linking: %v\n", *reactLinking)
	fmt.Printf("  Clarify Mode: %s\n", *enableClarify)
	fmt.Printf("  Log Mode: %s\n", *logMode)
	if *enableProofread {
		fmt.Printf("  Enable Proofread: true\n")
	}
	fmt.Printf("\n")

	// 1. 加载 dev.json
	examples, err := loadDevJSON(*devJSON)
	if err != nil {
		log.Fatalf("Failed to load dev.json: %v", err)
	}

	// 确定测试范围
	if *endIdx == -1 || *endIdx > len(examples) {
		*endIdx = len(examples)
	}
	examples = examples[*startIdx:*endIdx]

	fmt.Printf("Total examples: %d (range: [%d, %d))\n\n", len(examples), *startIdx, *endIdx)

	// 输出一次通用的 prompt 说明
	fmt.Println("═══════════════════════════════════════════════════════════════════════════════")
	fmt.Println("📋 System Configuration")
	fmt.Println("═══════════════════════════════════════════════════════════════════════════════")
	fmt.Printf("Model: %s\n", modelDisplayName)
	fmt.Printf("Use Rich Context: %v\n", *useRichContext)
	fmt.Printf("Use ReAct: %v\n", *useReact)
	fmt.Printf("React Linking: %v\n", *reactLinking)
	fmt.Printf("Clarify Mode: %s\n", *enableClarify)
	fmt.Printf("Enable Proofread: %v\n", *enableProofread)
	fmt.Printf("Log Mode: %s\n", *logMode)
	if *useReact {
		fmt.Println("\n📝 ReAct System Prompt (used for all samples):")
		fmt.Println("- SQL Best Practices: TEXT field casting, NULL handling, string matching, etc.")
		fmt.Println("- Available Tools: execute_sql, clarify_fields, update_rich_context")
		fmt.Println("- Iteration Limit: 5 effective iterations (update_rich_context doesn't count)")
		fmt.Println("- Validation Strategy: Use LIMIT or COUNT(*) for large result sets")
	}
	fmt.Println("═══════════════════════════════════════════════════════════════════════════════")
	fmt.Println()

	// 2. 初始化 LLM
	llmModel, err := llm.CreateLLMByType(modelTypeEnum)
	if err != nil {
		log.Fatalf("Failed to create LLM: %v", err)
	}

	// 2.1 询问模型身份（让模型自己输出）
	fmt.Println("\n🤖 Asking LLM to identify itself...")
	identityPrompt := "Please identify yourself. Output in the format: Model: [your model name and version]"
	identityResponse, err := llmModel.Call(context.Background(), identityPrompt)
	if err != nil {
		fmt.Printf("⚠️  Failed to get model identity: %v\n", err)
		fmt.Printf("📋 Using configured model name: %s\n\n", modelDisplayName)
	} else {
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("🤖 Model Self-Identification:")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println(identityResponse)
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()
	}

	// 3. 创建输出目录和文件（提前创建，用于增量写入）
	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("Failed to create output dir: %v", err)
	}

	// 打开 JSON 文件（追加模式）
	jsonPath := filepath.Join(*outputDir, "results.json")
	jsonFile, err := os.Create(jsonPath)
	if err != nil {
		log.Fatalf("Failed to create json file: %v", err)
	}
	defer jsonFile.Close()

	// 打开 SQL 文件（追加模式）
	sqlPath := filepath.Join(*outputDir, "predict.sql")
	sqlFile, err := os.Create(sqlPath)
	if err != nil {
		log.Fatalf("Failed to create sql file: %v", err)
	}
	defer sqlFile.Close()

	// 写入 JSON 数组开始
	if _, err := jsonFile.WriteString("[\n"); err != nil {
		log.Fatalf("Failed to write json header: %v", err)
	}

	// 确保程序退出时正确关闭 JSON 数组（包括被 kill 的情况）
	// 使用 signal 捕获来处理 SIGTERM/SIGINT
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n⚠️  Received interrupt signal, closing files gracefully...")
		jsonFile.WriteString("\n]\n")
		jsonFile.Close()
		sqlFile.Close()
		os.Exit(0)
	}()

	defer func() {
		jsonFile.Close()
		sqlFile.Close()
	}()

	// 3. 逐个评估（增量写入，避免 OOM）
	// 只保留统计信息，不保存完整结果
	var (
		successCount  int
		totalTime     float64
		totalLLMCalls int
		totalTokens   int
		totalClarify  int
	)
	ctx := context.Background()

	// 记录初始内存状态
	var initialMem runtime.MemStats
	runtime.ReadMemStats(&initialMem)
	fmt.Printf("\n[Memory] Initial - Alloc: %d MB, Sys: %d MB\n\n",
		initialMem.Alloc/1024/1024, initialMem.Sys/1024/1024)

	for i, example := range examples {
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Printf("[%d/%d] DB: %s\n", i+1, len(examples), example.DbID)
		fmt.Printf("Question: %s\n", example.Question)
		fmt.Printf("Gold SQL: %s\n", example.Query)

		result := evaluateExample(ctx, llmModel, example, *dbDir, *contextDir, *useRichContext, *useReact, *reactLinking, *enableClarify, *enableProofread, *logMode)

		// 更新统计信息
		if result.Status == "success" {
			successCount++
		}
		totalTime += result.TimeSeconds
		totalLLMCalls += result.LLMCalls
		totalTokens += result.TotalTokens
		totalClarify += result.ClarifyCount

		// 立即写入 JSON（增量写入）
		if i > 0 {
			if _, err := jsonFile.WriteString(",\n"); err != nil {
				log.Printf("Failed to write json separator: %v", err)
			}
		}
		jsonData, err := json.MarshalIndent(result, "  ", "  ")
		if err != nil {
			log.Printf("Failed to marshal result: %v", err)
		} else {
			if _, err := jsonFile.WriteString("  " + string(jsonData)); err != nil {
				log.Printf("Failed to write json result: %v", err)
			}
			// 立即 flush 到磁盘
			jsonFile.Sync()
		}

		// 立即写入 SQL（增量写入）
		sql := result.GeneratedSQL
		if sql == "" {
			sql = "SELECT 1" // 失败的情况用占位符
		}
		sql = strings.TrimSpace(sql)
		sql = strings.TrimSuffix(sql, ";")
		sql = strings.ReplaceAll(sql, "\n", " ")
		sql = strings.ReplaceAll(sql, "\r", " ")
		sql = strings.Join(strings.Fields(sql), " ")
		if _, err := fmt.Fprintf(sqlFile, "%s\t%s\n", sql, result.DbID); err != nil {
			log.Printf("Failed to write sql: %v", err)
		}

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

		// 每个样本后都强制 GC，防止内存累积
		runtime.GC()

		// 每 50 个样本打印详细内存分析
		if (i+1)%50 == 0 {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)

			fmt.Println("\n" + strings.Repeat("=", 80))
			fmt.Printf("[Memory Analysis] Sample %d/%d\n", i+1, len(examples))
			fmt.Println(strings.Repeat("=", 80))

			// 基本内存统计
			fmt.Printf("Heap Alloc:      %6d MB (当前堆上分配的内存)\n", m.Alloc/1024/1024)
			fmt.Printf("Total Alloc:     %6d MB (累计分配的内存)\n", m.TotalAlloc/1024/1024)
			fmt.Printf("Sys:             %6d MB (从系统获取的内存)\n", m.Sys/1024/1024)
			fmt.Printf("Heap Sys:        %6d MB (堆内存总量)\n", m.HeapSys/1024/1024)
			fmt.Printf("Heap Idle:       %6d MB (空闲堆内存)\n", m.HeapIdle/1024/1024)
			fmt.Printf("Heap In Use:     %6d MB (正在使用的堆内存)\n", m.HeapInuse/1024/1024)
			fmt.Printf("Heap Released:   %6d MB (已释放给OS的内存)\n", m.HeapReleased/1024/1024)
			fmt.Printf("Stack In Use:    %6d MB (栈内存使用)\n", m.StackInuse/1024/1024)

			// GC 统计
			fmt.Printf("\nGC Runs:         %6d 次\n", m.NumGC)
			fmt.Printf("GC Pause Total:  %6d ms\n", m.PauseTotalNs/1000000)
			if m.NumGC > 0 {
				fmt.Printf("Last GC Pause:   %6d ms\n", m.PauseNs[(m.NumGC+255)%256]/1000000)
			}

			// 对象统计
			fmt.Printf("\nHeap Objects:    %6d 个\n", m.HeapObjects)
			fmt.Printf("Mallocs:         %6d 次 (总分配次数)\n", m.Mallocs)
			fmt.Printf("Frees:           %6d 次 (总释放次数)\n", m.Frees)
			fmt.Printf("Live Objects:    %6d 个 (Mallocs - Frees)\n", m.Mallocs-m.Frees)

			// 内存增长分析
			growth := float64(m.Alloc-initialMem.Alloc) / float64(i+1) / 1024 / 1024
			fmt.Printf("\nAvg Growth:      %.2f MB/sample\n", growth)
			projected := initialMem.Alloc/1024/1024 + uint64(float64(len(examples))*growth)
			fmt.Printf("Projected Peak:  %d MB (at %d samples)\n", projected, len(examples))

			fmt.Println(strings.Repeat("=", 80) + "\n")
		}

		// 每 10 个样本打印简要内存信息
		if (i+1)%10 == 0 && (i+1)%50 != 0 {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			fmt.Printf("[Memory] Sample %d - Alloc: %d MB, HeapInUse: %d MB\n",
				i+1, m.Alloc/1024/1024, m.HeapInuse/1024/1024)
		}
	}

	// 写入 JSON 数组结束
	if _, err := jsonFile.WriteString("\n]\n"); err != nil {
		log.Printf("Failed to write json footer: %v", err)
	}

	// 4. 打印统计结果
	fmt.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("📊 Evaluation Summary")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("Total: %d\n", len(examples))
	fmt.Printf("Success: %d (%.1f%%)\n", successCount, float64(successCount)/float64(len(examples))*100)
	fmt.Printf("Failed: %d\n", len(examples)-successCount)
	if len(examples) > 0 {
		fmt.Printf("Avg Time: %.2fs\n", totalTime/float64(len(examples)))
		fmt.Printf("Avg LLM Calls: %.1f\n", float64(totalLLMCalls)/float64(len(examples)))
		fmt.Printf("Total Tokens: %d (Avg: %d per query)\n", totalTokens, totalTokens/len(examples))
	}
	if totalClarify > 0 {
		fmt.Printf("Total Clarifications: %d (%.1f%%)\n", totalClarify, float64(totalClarify)/float64(len(examples))*100)
	}

	fmt.Printf("\n✓ Results saved to: %s/\n", *outputDir)
	fmt.Printf("  - results.json (详细结果)\n")
	fmt.Printf("  - predict.sql (预测SQL，用于Spider评测)\n")
}

type EvalResult struct {
	DbID           string   `json:"db_id"`
	Question       string   `json:"question"`
	GoldSQL        string   `json:"gold_sql"`
	GeneratedSQL   string   `json:"generated_sql"`
	Status         string   `json:"status"` // success, error, timeout
	Error          string   `json:"error,omitempty"`
	TimeSeconds    float64  `json:"time_seconds"`
	LLMCalls       int      `json:"llm_calls"`
	TotalTokens    int      `json:"total_tokens"`
	ClarifyCount   int      `json:"clarify_count"`
	SelectedTables []string `json:"selected_tables"`
}

func evaluateExample(
	ctx context.Context,
	llm llms.Model,
	example SpiderExample,
	dbDir string,
	contextDir string,
	useRichContext bool,
	useReact bool,
	reactLinking bool,
	enableClarify string,
	enableProofread bool,
	logMode string,
) EvalResult {
	result := EvalResult{
		DbID:     example.DbID,
		Question: example.Question,
		GoldSQL:  example.Query,
		Status:   "error",
	}

	// 1. 创建数据库 adapter
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

	// 2. 构建 context 文件路径
	var contextFile string
	if useRichContext {
		contextFile = filepath.Join(contextDir, example.DbID+".json")
		// 检查文件是否存在
		if _, err := os.Stat(contextFile); os.IsNotExist(err) {
			result.Error = fmt.Sprintf("context file not found: %s", contextFile)
			return result
		}
	}

	// 3. 创建推理管线
	pipelineConfig := &inference.Config{
		UseRichContext:          useRichContext,
		UseReact:                useReact,
		ReactLinking:            reactLinking,
		UseDryRun:               false, // Spider 评估不需要 dry run
		MaxIterations:           20,    // 实际上限20次（冗余），但告诉LLM只有5次（不含update_rich_context）
		ContextFile:             contextFile,
		ClarifyMode:             enableClarify,
		LogMode:                 logMode,
		ResultFields:            example.ResultFields,
		ResultFieldsDescription: example.ResultFieldsDescription,
		EnableProofread:         enableProofread,
		DBName:                  example.DbID,
		DBType:                  "sqlite",
	}

	pipeline := inference.NewPipeline(llm, dbAdapter, pipelineConfig)

	// 4. 执行推理
	inferResult, err := pipeline.Execute(ctx, example.Question)
	if err != nil {
		result.Error = fmt.Sprintf("inference: %v", err)
		return result
	}

	// 5. 记录结果
	result.GeneratedSQL = inferResult.GeneratedSQL
	result.LLMCalls = inferResult.LLMCalls
	result.TotalTokens = inferResult.TotalTokens
	result.ClarifyCount = inferResult.ClarifyCount
	result.SelectedTables = inferResult.SelectedTables
	result.TimeSeconds = inferResult.TotalTime.Seconds()
	result.Status = "success"

	return result
}

func loadDevJSON(path string) ([]SpiderExample, error) {
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
