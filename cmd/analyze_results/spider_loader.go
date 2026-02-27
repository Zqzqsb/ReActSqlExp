package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// SpiderResult represents Spider evaluation result format
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
	Difficulty     string   `json:"difficulty,omitempty"`
}

// LoadSpiderResultFile loads Spider evaluation result file
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
			ID:         i + 1,
			DBName:     sr.DbID,
			Question:   sr.Question,
			GTSQL:      sr.GoldSQL,
			PredSQL:    sr.GeneratedSQL,
			Difficulty: sr.Difficulty,
		})
	}

	return results, nil
}
