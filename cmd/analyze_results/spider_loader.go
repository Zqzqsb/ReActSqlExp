package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// SpiderResult Spider评测结果格式
type SpiderResult struct {
	DbID           string   `json:"db_id"`
	Question       string   `json:"question"`
	GoldSQL        string   `json:"gold_sql"`
	GeneratedSQL   string   `json:"generated_sql"`
	Status         string   `json:"status"`
	Error          string   `json:"error,omitempty"`
	TimeSeconds    float64  `json:"time_seconds"`
	LLMCalls       int      `json:"llm_calls"`
	SelectedTables []string `json:"selected_tables"`
}

// LoadSpiderResultFile 加载Spider评测结果文件
func LoadSpiderResultFile(filePath string) ([]InputResult, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %v", err)
	}

	var spiderResults []SpiderResult
	if err := json.Unmarshal(data, &spiderResults); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %v", err)
	}

	// Convert to InputResult format
	results := make([]InputResult, 0, len(spiderResults))
	for i, sr := range spiderResults {
		results = append(results, InputResult{
			ID:       i + 1,
			DBName:   sr.DbID,
			Question: sr.Question,
			GTSQL:    sr.GoldSQL,
			PredSQL:  sr.GeneratedSQL,
		})
	}

	return results, nil
}
