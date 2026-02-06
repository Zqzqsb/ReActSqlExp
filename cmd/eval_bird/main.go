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
	"time"

	"github.com/tmc/langchaingo/llms"
	"reactsql/internal/adapter"
	"reactsql/internal/inference"
	"reactsql/internal/llm"
)

// BirdExample BIRD数据集样例
type BirdExample struct {
	QuestionID int    `json:"question_id"`
	DbID       string `json:"db_id"`
	Question   string `json:"question"`
	Evidence   string `json:"evidence"`
	SQL        string `json:"SQL"`
	Difficulty string `json:"difficulty"`
}

// EvalResult 评测结果
type EvalResult struct {
	QuestionID     int      `json:"question_id"`
	DbID           string   `json:"db_id"`
	Question       string   `json:"question"`
	Evidence       string   `json:"evidence"`
	GoldSQL        string   `json:"gold_sql"`
	GeneratedSQL   string   `json:"generated_sql"`
	Status         string   `json:"status"` // success, error, timeout
	Error          string   `json:"error,omitempty"`
	TimeSeconds    float64  `json:"time_seconds"`
	LLMCalls       int      `json:"llm_calls"`
	SelectedTables []string `json:"selected_tables"`
	Difficulty     string   `json:"difficulty"`
}

func main() {
	// 命令行参数
	devJSON := flag.String("dev", "benchmarks/bird/dev/dev.json", "dev.json 路径")
	dbDir := flag.String("db-dir", "benchmarks/bird/dev/dev_databases", "数据库目录")
	contextDir := flag.String("context-dir", "contexts/sqlite/bird", "Rich Context 目录")
	outputDir := flag.String("output-dir", "", "结果输出目录（为空则自动生成时间戳目录）")
	modelType := flag.String("model", "deepseek-v3", "模型类型: deepseek-v3 | deepseek-v3.2 | qwen-max | qwen3-max | ali-deepseek-v3.2")
	useRichContext := flag.Bool("use-rich-context", false, "是否使用Rich Context")
	useReact := flag.Bool("use-react", false, "是否使用ReAct循环")
	limit := flag.Int("limit", 0, "限制评测样例数量（0表示全部）")
	difficulty := flag.String("difficulty", "", "只评测指定难度（simple/moderate/challenging）")

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
	fmt.Printf("使用模型: %s\n", modelDisplayName)

	// 加载BIRD dev.json
	examples, err := loadBirdExamples(*devJSON)
	if err != nil {
		log.Fatalf("加载BIRD样例失败: %v", err)
	}

	fmt.Printf("加载了 %d 个BIRD样例\n", len(examples))

	// 过滤难度
	if *difficulty != "" {
		filtered := []BirdExample{}
		for _, ex := range examples {
			if ex.Difficulty == *difficulty {
				filtered = append(filtered, ex)
			}
		}
		examples = filtered
		fmt.Printf("过滤后剩余 %d 个样例（难度=%s）\n", len(examples), *difficulty)
	}

	// 限制数量
	if *limit > 0 && *limit < len(examples) {
		examples = examples[:*limit]
		fmt.Printf("限制评测数量为 %d\n", *limit)
	}

	// 创建输出目录
	if *outputDir == "" {
		timestamp := time.Now().Format("20060102_150405")
		mode := "baseline"
		if *useRichContext {
			mode = "rich_context"
		}
		if *useReact {
			mode += "_react"
		}
		*outputDir = filepath.Join("results", "bird", timestamp+"_"+mode)
	}

	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		log.Fatalf("创建输出目录失败: %v", err)
	}

	fmt.Printf("输出目录: %s\n", *outputDir)

	// 初始化LLM
	llmModel, err := llm.CreateLLMByType(modelTypeEnum)
	if err != nil {
		log.Fatalf("初始化LLM失败: %v", err)
	}

	// 评测
	ctx := context.Background()
	results := make([]EvalResult, 0, len(examples))

	for i, example := range examples {
		fmt.Printf("\n[%d/%d] 评测问题 %d: %s\n", i+1, len(examples), example.QuestionID, example.Question)

		result := evaluateExample(ctx, llmModel, example, *dbDir, *contextDir, *useRichContext, *useReact)
		results = append(results, result)

		fmt.Printf("  状态: %s, 耗时: %.2fs, LLM调用: %d\n", result.Status, result.TimeSeconds, result.LLMCalls)
		if result.Error != "" {
			fmt.Printf("  错误: %s\n", result.Error)
		}
	}

	// 保存结果
	if err := saveResults(*outputDir, results); err != nil {
		log.Fatalf("保存结果失败: %v", err)
	}

	// 统计
	printSummary(results)
}

func loadBirdExamples(filePath string) ([]BirdExample, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var examples []BirdExample
	if err := json.Unmarshal(data, &examples); err != nil {
		return nil, err
	}

	return examples, nil
}

func evaluateExample(
	ctx context.Context,
	llmModel llms.Model,
	example BirdExample,
	dbDir string,
	contextDir string,
	useRichContext bool,
	useReact bool,
) EvalResult {
	result := EvalResult{
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

	// 1. 创建数据库adapter
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

	// 2. 构建context文件路径
	var contextFile string
	if useRichContext {
		contextFile = filepath.Join(contextDir, example.DbID+".json")
		// 检查文件是否存在
		if _, err := os.Stat(contextFile); os.IsNotExist(err) {
			// BIRD可能没有rich context，不报错
			contextFile = ""
		}
	}

	// 3. 构建完整问题（包含evidence）
	question := example.Question
	if example.Evidence != "" {
		question = fmt.Sprintf("%s\nEvidence: %s", example.Question, example.Evidence)
	}

	// 4. 创建推理管线
	pipelineConfig := &inference.Config{
		UseRichContext: useRichContext && contextFile != "",
		UseReact:       useReact,
		UseDryRun:      false,
		MaxIterations:  5,
		ContextFile:    contextFile,
	}

	pipeline := inference.NewPipeline(llmModel, dbAdapter, pipelineConfig)

	// 5. 执行推理
	inferResult, err := pipeline.Execute(ctx, question)
	if err != nil {
		result.Error = fmt.Sprintf("inference: %v", err)
		return result
	}

	// 6. 记录结果
	result.GeneratedSQL = inferResult.GeneratedSQL
	result.LLMCalls = inferResult.LLMCalls
	result.SelectedTables = inferResult.SelectedTables
	result.Status = "success"

	return result
}

func saveResults(outputDir string, results []EvalResult) error {
	// 保存JSON格式
	jsonPath := filepath.Join(outputDir, "results.json")
	jsonData, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %v", err)
	}
	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		return fmt.Errorf("write json: %v", err)
	}

	// 保存SQL格式（用于官方评测）
	sqlPath := filepath.Join(outputDir, "predict.sql")
	var sqlBuilder strings.Builder
	for _, result := range results {
		// 压缩SQL为单行
		sql := strings.ReplaceAll(result.GeneratedSQL, "\n", " ")
		sql = strings.TrimSpace(sql)
		sqlBuilder.WriteString(sql)
		sqlBuilder.WriteString("\t")
		sqlBuilder.WriteString(result.DbID)
		sqlBuilder.WriteString("\n")
	}
	if err := os.WriteFile(sqlPath, []byte(sqlBuilder.String()), 0644); err != nil {
		return fmt.Errorf("write sql: %v", err)
	}

	fmt.Printf("\n结果已保存:\n")
	fmt.Printf("  JSON: %s\n", jsonPath)
	fmt.Printf("  SQL:  %s\n", sqlPath)

	return nil
}

func printSummary(results []EvalResult) {
	total := len(results)
	success := 0
	errors := 0

	difficultyStats := make(map[string]int)
	difficultySuccess := make(map[string]int)

	for _, r := range results {
		if r.Status == "success" {
			success++
			difficultySuccess[r.Difficulty]++
		} else {
			errors++
		}
		difficultyStats[r.Difficulty]++
	}

	fmt.Printf("\n========== 评测摘要 ==========\n")
	fmt.Printf("总样例数: %d\n", total)
	fmt.Printf("成功: %d (%.2f%%)\n", success, float64(success)/float64(total)*100)
	fmt.Printf("失败: %d (%.2f%%)\n", errors, float64(errors)/float64(total)*100)

	fmt.Printf("\n按难度统计:\n")
	for _, diff := range []string{"simple", "moderate", "challenging"} {
		if count, ok := difficultyStats[diff]; ok {
			succ := difficultySuccess[diff]
			fmt.Printf("  %s: %d/%d (%.2f%%)\n", diff, succ, count, float64(succ)/float64(count)*100)
		}
	}
}
