package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// DevQuestion represents question structure in dev.json
type DevQuestion struct {
	DbID    string `json:"db_id"`
	Query   string `json:"query"`
	SPJType string `json:"spj_type,omitempty"`
}

// LoadSPJTags loads SPJ tags from dev.json
// Returns map[question_id]spj_type
func LoadSPJTags(devJSONPath string) (map[int]string, error) {
	data, err := os.ReadFile(devJSONPath)
	if err != nil {
		// If file not found, return empty map (not an error)
		return make(map[int]string), nil
	}

	var devQuestions []DevQuestion
	if err := json.Unmarshal(data, &devQuestions); err != nil {
		return nil, fmt.Errorf("failed to parse dev.json: %v", err)
	}

	spjTags := make(map[int]string)
	for i, q := range devQuestions {
		if q.SPJType != "" && q.SPJType != "null" {
			spjTags[i] = q.SPJType
		}
	}

	if len(spjTags) > 0 {
		fmt.Printf("✅ Loaded from dev.json: %d  SPJ tags\n", len(spjTags))
		// Print first few SPJ tag indices
		indices := make([]int, 0, len(spjTags))
		for idx := range spjTags {
			indices = append(indices, idx)
		}
		fmt.Printf("   SPJ tag indices (first 10): %v\n", indices[:min(10, len(indices))])
	}

	return spjTags, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// MergeSPJTags merges SPJ tags into InputResult
func MergeSPJTags(results []InputResult, spjTags map[int]string) {
	mergedCount := 0
	for i := range results {
		if spjType, exists := spjTags[i]; exists {
			results[i].SPJType = spjType
			mergedCount++
		}
	}
	if mergedCount > 0 {
		fmt.Printf("✅ Merged %d SPJ tags into InputResult\n", mergedCount)
	}
}
