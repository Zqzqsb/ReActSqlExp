package main

import (
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

// SpiderCase Spider 数据集的一个 case
type SpiderCase struct {
	DBId     string   `json:"db_id"`
	Question string   `json:"question"`
	Query    string   `json:"query"`
	QueryTok []string `json:"query_tok,omitempty"`

	// 新增字段
	ResultFields            []string `json:"result_fields,omitempty"`
	ResultFieldsDescription string   `json:"result_fields_description,omitempty"`
}

func main() {
	inputFile := flag.String("input", "benchmarks/spider/dev.json", "输入文件路径")
	outputFile := flag.String("output", "benchmarks/spider/dev_with_fields.json", "输出文件路径")
	useV32 := flag.Bool("v3.2", false, "使用 DeepSeek-V3.2 模型（默认使用 V3）")
	flag.Parse()

	fmt.Println("🚀 Extract Result Fields from Gold SQL")
	fmt.Printf("📁 Input: %s\n", *inputFile)
	fmt.Printf("📁 Output: %s\n", *outputFile)
	fmt.Printf("🤖 Model: %s\n\n", llm.GetModelName(*useV32))

	// 1. 读取数据集
	data, err := os.ReadFile(*inputFile)
	if err != nil {
		log.Fatalf("Failed to read input file: %v", err)
	}

	var cases []SpiderCase
	if err := json.Unmarshal(data, &cases); err != nil {
		log.Fatalf("Failed to parse JSON: %v", err)
	}

	fmt.Printf("📊 Total cases: %d\n\n", len(cases))

	// 2. 创建 LLM
	llmInstance, err := llm.CreateLLMWithFlag(*useV32)
	if err != nil {
		log.Fatalf("Failed to create LLM: %v", err)
	}

	ctx := context.Background()

	// 3. 处理每个 case
	for i := range cases {
		fmt.Printf("[%d/%d] Processing: %s\n", i+1, len(cases), cases[i].DBId)

		// 提取字段
		fields, description, err := extractResultFields(ctx, llmInstance, cases[i].Question, cases[i].Query)
		if err != nil {
			fmt.Printf("  ⚠️  Failed: %v\n", err)
			continue
		}

		cases[i].ResultFields = fields
		cases[i].ResultFieldsDescription = description

		fmt.Printf("  ✓ Fields: %v\n", fields)
		fmt.Printf("  ✓ Description: %s\n\n", description)
	}

	// 4. 保存结果
	output, err := json.MarshalIndent(cases, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal JSON: %v", err)
	}

	if err := os.WriteFile(*outputFile, output, 0644); err != nil {
		log.Fatalf("Failed to write output file: %v", err)
	}

	fmt.Printf("✅ Results saved to: %s\n", *outputFile)
}

// extractResultFields 从 SQL 中提取结果字段
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

	// 解析响应
	response = strings.TrimSpace(response)

	// 移除可能的 markdown 代码块
	response = strings.TrimPrefix(response, "```json")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	var result struct {
		Fields            []string `json:"fields"`
		FieldDescriptions []string `json:"field_descriptions"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, "", fmt.Errorf("failed to parse LLM response: %w", err)
	}

	// 将字段描述合并为一个字符串
	description := strings.Join(result.FieldDescriptions, "; ")

	return result.Fields, description, nil
}
