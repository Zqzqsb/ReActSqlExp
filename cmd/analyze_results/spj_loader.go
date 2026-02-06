package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// DevQuestion dev.json 中的问题结构
type DevQuestion struct {
	DbID    string `json:"db_id"`
	Query   string `json:"query"`
	SPJType string `json:"spj_type,omitempty"`
}

// LoadSPJTags 从 dev.json 加载 SPJ 标签
// 返回一个 map[question_id]spj_type
func LoadSPJTags(devJSONPath string) (map[int]string, error) {
	data, err := os.ReadFile(devJSONPath)
	if err != nil {
		// 如果文件不存在，返回空 map（不是错误）
		return make(map[int]string), nil
	}

	var devQuestions []DevQuestion
	if err := json.Unmarshal(data, &devQuestions); err != nil {
		return nil, fmt.Errorf("解析 dev.json 失败: %v", err)
	}

	spjTags := make(map[int]string)
	for i, q := range devQuestions {
		if q.SPJType != "" && q.SPJType != "null" {
			spjTags[i] = q.SPJType
		}
	}

	if len(spjTags) > 0 {
		fmt.Printf("✅ 从 dev.json 加载了 %d 个 SPJ 标签\n", len(spjTags))
		// 打印前几个 SPJ 标签的索引
		indices := make([]int, 0, len(spjTags))
		for idx := range spjTags {
			indices = append(indices, idx)
		}
		fmt.Printf("   SPJ 标签索引 (前10个): %v\n", indices[:min(10, len(indices))])
	}

	return spjTags, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// MergeSPJTags 将 SPJ 标签合并到 InputResult 中
func MergeSPJTags(results []InputResult, spjTags map[int]string) {
	mergedCount := 0
	for i := range results {
		if spjType, exists := spjTags[i]; exists {
			results[i].SPJType = spjType
			mergedCount++
		}
	}
	if mergedCount > 0 {
		fmt.Printf("✅ 成功合并 %d 个 SPJ 标签到 InputResult\n", mergedCount)
	}
}
