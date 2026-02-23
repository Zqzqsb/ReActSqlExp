package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/tmc/langchaingo/llms"
	"reactsql/internal/llm"
)

// SpiderCase represents a case from Spider dataset
type SpiderCase struct {
	DBId     string   `json:"db_id"`
	Question string   `json:"question"`
	Query    string   `json:"query"`
	QueryTok []string `json:"query_tok,omitempty"`
	ID       int      `json:"id,omitempty"`

	// Additional fields
	ResultFields            []string `json:"result_fields,omitempty"`
	ResultFieldsDescription string   `json:"result_fields_description,omitempty"`
}

// BirdCase represents a case from BIRD dataset
type BirdCase struct {
	QuestionID int    `json:"question_id"`
	DbID       string `json:"db_id"`
	Question   string `json:"question"`
	Evidence   string `json:"evidence"`
	SQL        string `json:"SQL"`
	Difficulty string `json:"difficulty"`

	// Additional fields
	ResultFields            []string `json:"result_fields,omitempty"`
	ResultFieldsDescription string   `json:"result_fields_description,omitempty"`
}

func main() {
	benchmark := flag.String("benchmark", "", "Benchmark: spider | bird (if empty, will ask interactively)")
	modelType := flag.String("model", "deepseek-v3", "Model: deepseek-v3 | deepseek-v3.2 | qwen-max | qwen3-max | ali-deepseek-v3.2")
	inputFile := flag.String("input", "", "Input file path (auto-detected)")
	outputFile := flag.String("output", "", "Output file path (auto-detected)")
	limit := flag.Int("limit", 0, "Limit number of examples (0 = all)")
	flag.Parse()

	reader := bufio.NewReader(os.Stdin)

	// Step 1: Select benchmark
	if *benchmark == "" {
		fmt.Println()
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("📋 Generate Field Descriptions")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("  Extract result fields and descriptions from Gold SQL")
		fmt.Println()
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("📦 Select Benchmark")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("  1. spider  — Spider dev set")
		fmt.Println("  2. bird    — BIRD dev set")
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

	// Step 2: Select model (if not provided via flag)
	if *modelType == "deepseek-v3" && flag.NFlag() == 0 {
		fmt.Println()
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("🤖 Select Model")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

		cfg := llm.GetConfig()
		modelOptions := []struct {
			key         string
			displayName string
			modelName   string
		}{
			{"deepseek-v3", "DeepSeek-V3 (Volcano)", cfg.DeepSeekV3.ModelName},
			{"deepseek-v3.2", "DeepSeek-V3.2 (Volcano)", cfg.DeepSeekV32.ModelName},
			{"qwen-max", "Qwen-Max (Aliyun)", cfg.QwenMax.ModelName},
			{"qwen3-max", "Qwen3-Max (Aliyun)", cfg.Qwen3Max.ModelName},
			{"ali-deepseek-v3.2", "DeepSeek-V3.2 (Aliyun)", cfg.AliDeepSeek.ModelName},
		}

		for i, opt := range modelOptions {
			fmt.Printf("  %d. %-25s — %s\n", i+1, opt.displayName, opt.modelName)
		}
		fmt.Println()
		fmt.Print("Enter choice [1-5] (default: 1): ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			input = "1"
		}

		var choice int
		if _, err := fmt.Sscanf(input, "%d", &choice); err != nil || choice < 1 || choice > len(modelOptions) {
			log.Fatalf("Invalid choice: %s", input)
		}

		*modelType = modelOptions[choice-1].key
	}

	// Step 3: Resolve paths
	defaultPaths := map[string]map[string]string{
		"spider": {
			"input":  "benchmarks/spider_corrected/dev_with_field_with_id.json",
			"output": "benchmarks/spider_corrected/dev_with_field_with_id_updated.json",
		},
		"bird": {
			"input":  "benchmarks/bird/dev/dev.json",
			"output": "benchmarks/bird/dev/dev_with_fields.json",
		},
	}

	if *inputFile == "" {
		*inputFile = defaultPaths[*benchmark]["input"]
	}
	if *outputFile == "" {
		*outputFile = defaultPaths[*benchmark]["output"]
	}

	// Parse model type
	modelTypeEnum := parseModelType(*modelType)
	modelDisplayName := llm.GetModelDisplayName(modelTypeEnum)

	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("🚀 Generate Field Descriptions — %s\n", strings.ToUpper(*benchmark))
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Input:  %s\n", *inputFile)
	fmt.Printf("  Output: %s\n", *outputFile)
	fmt.Printf("  Model:  %s\n", modelDisplayName)
	if *limit > 0 {
		fmt.Printf("  Limit:  %d examples\n", *limit)
	}
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	// Create LLM
	llmInstance, err := llm.CreateLLMByType(modelTypeEnum)
	if err != nil {
		log.Fatalf("Failed to create LLM: %v", err)
	}

	ctx := context.Background()

	// Process based on benchmark
	switch *benchmark {
	case "spider":
		processSpider(ctx, llmInstance, *inputFile, *outputFile, *limit)
	case "bird":
		processBird(ctx, llmInstance, *inputFile, *outputFile, *limit)
	}
}

func processSpider(ctx context.Context, llm llms.Model, inputFile, outputFile string, limit int) {
	// Read dataset
	data, err := os.ReadFile(inputFile)
	if err != nil {
		log.Fatalf("Failed to read input file: %v", err)
	}

	var cases []SpiderCase
	if err := json.Unmarshal(data, &cases); err != nil {
		log.Fatalf("Failed to parse JSON: %v", err)
	}

	if limit > 0 && limit < len(cases) {
		cases = cases[:limit]
	}

	fmt.Printf("📊 Total cases: %d\n\n", len(cases))

	// Process each case
	for i := range cases {
		fmt.Printf("[%d/%d] DB: %s\n", i+1, len(cases), cases[i].DBId)

		// Skip if already has fields
		if len(cases[i].ResultFields) > 0 {
			fmt.Printf("  ⏭️  Already has fields, skipping\n\n")
			continue
		}

		fields, description, err := extractResultFields(ctx, llm, cases[i].Question, cases[i].Query)
		if err != nil {
			fmt.Printf("  ⚠️  Failed: %v\n\n", err)
			continue
		}

		cases[i].ResultFields = fields
		cases[i].ResultFieldsDescription = description

		fmt.Printf("  ✓ Fields: %v\n", fields)
		fmt.Printf("  ✓ Description: %s\n\n", description)
	}

	// Save results
	output, err := json.MarshalIndent(cases, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal JSON: %v", err)
	}

	if err := os.WriteFile(outputFile, output, 0644); err != nil {
		log.Fatalf("Failed to write output file: %v", err)
	}

	fmt.Printf("✅ Results saved to: %s\n", outputFile)
}

func processBird(ctx context.Context, llm llms.Model, inputFile, outputFile string, limit int) {
	// Read dataset
	data, err := os.ReadFile(inputFile)
	if err != nil {
		log.Fatalf("Failed to read input file: %v", err)
	}

	var cases []BirdCase
	if err := json.Unmarshal(data, &cases); err != nil {
		log.Fatalf("Failed to parse JSON: %v", err)
	}

	if limit > 0 && limit < len(cases) {
		cases = cases[:limit]
	}

	fmt.Printf("📊 Total cases: %d\n\n", len(cases))

	// Process each case
	for i := range cases {
		fmt.Printf("[%d/%d] DB: %s (Q%d)\n", i+1, len(cases), cases[i].DbID, cases[i].QuestionID)

		// Skip if already has fields
		if len(cases[i].ResultFields) > 0 {
			fmt.Printf("  ⏭️  Already has fields, skipping\n\n")
			continue
		}

		// Build full question with evidence
		question := cases[i].Question
		if cases[i].Evidence != "" {
			question = fmt.Sprintf("%s\nEvidence: %s", cases[i].Question, cases[i].Evidence)
		}

		fields, description, err := extractResultFields(ctx, llm, question, cases[i].SQL)
		if err != nil {
			fmt.Printf("  ⚠️  Failed: %v\n\n", err)
			continue
		}

		cases[i].ResultFields = fields
		cases[i].ResultFieldsDescription = description

		fmt.Printf("  ✓ Fields: %v\n", fields)
		fmt.Printf("  ✓ Description: %s\n\n", description)
	}

	// Save results
	output, err := json.MarshalIndent(cases, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal JSON: %v", err)
	}

	if err := os.WriteFile(outputFile, output, 0644); err != nil {
		log.Fatalf("Failed to write output file: %v", err)
	}

	fmt.Printf("✅ Results saved to: %s\n", outputFile)
}

// extractResultFields extracts result fields from SQL
func extractResultFields(ctx context.Context, llm llms.Model, question string, sql string) ([]string, string, error) {
	prompt := fmt.Sprintf(`Analyze the SQL query and extract the result fields.

Question: %s

SQL Query:
%s

Task:
1. Extract all fields from SELECT clause in EXACT ORDER as they appear in SQL
2. Use alias if present, otherwise use column name WITHOUT table prefix (e.g., "Name" not "singer.Name")
3. For each field, provide a brief description (5-10 words) of what it represents

IMPORTANT:
- Field order MUST match SQL SELECT clause order exactly
- Field names should be clean (no table prefix like "t1." or "singer.")
- Descriptions should be concise and specific to the question context
- For expressions like COUNT(*), use the alias or the expression itself

Output format (JSON):
{
  "fields": ["field1", "field2", ...],
  "field_descriptions": [
    "field1: description of field1",
    "field2: description of field2"
  ]
}

Output:`, question, sql)

	response, err := llm.Call(ctx, prompt)
	if err != nil {
		return nil, "", err
	}

	// Parse response
	response = strings.TrimSpace(response)

	// Remove possible markdown code blocks
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	var result struct {
		Fields            []string `json:"fields"`
		FieldDescriptions []string `json:"field_descriptions"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, "", fmt.Errorf("failed to parse LLM response: %w\nResponse: %s", err, response)
	}

	// Merge field descriptions into a string
	description := strings.Join(result.FieldDescriptions, "; ")

	return result.Fields, description, nil
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
