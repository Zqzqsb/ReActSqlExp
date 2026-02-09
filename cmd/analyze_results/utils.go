package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reactsql/internal/adapter"
)

// LoadInputFile 从文件加载输入结果
func LoadInputFile(filePath string) ([]InputResult, error) {
	// Check file extension to determine format
	if strings.HasSuffix(filePath, ".json") && !strings.HasSuffix(filePath, ".jsonl") {
		return LoadSpiderResultFile(filePath)
	}

	// JSONL format
	fileContent, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %v", err)
	}

	var results []InputResult
	scanner := bufio.NewScanner(strings.NewReader(string(fileContent)))

	// 设置更大的缓冲区以处理长JSON行 (10MB)
	const maxCapacity = 30 * 1024 * 1024 // 30MB
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var result InputResult
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			continue
		}
		results = append(results, result)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read file: %v", err)
	}

	return results, nil
}

// LoadSingleResultFile 从单个JSON文件加载结果
func LoadSingleResultFile(filePath string) (*InputResult, error) {
	// 读取文件
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %v", err)
	}

	// 解析JSON
	var result InputResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("解析JSON失败: %v", err)
	}

	return &result, nil
}

// LoadResultsFromDirectory 从目录加载所有结果文件
func LoadResultsFromDirectory(dirPath string) ([]InputResult, error) {
	// 检查目录是否存在
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("目录不存在: %s", dirPath)
	}

	// 首先检查目录中是否有info.jsonl文件直接加载
	jsonlPath := filepath.Join(dirPath, "info.jsonl")
	if _, err := os.Stat(jsonlPath); err == nil {
		return LoadInputFile(jsonlPath)
	}

	var results []InputResult
	// 使用集合记录已处理的文件，避免重复
	processedIDs := make(map[int]bool)

	// 遍历目录中的所有JSON文件
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 只处理JSON文件且跳过隐藏文件
		if !info.IsDir() &&
			(strings.HasSuffix(info.Name(), ".json") || strings.HasSuffix(info.Name(), ".jsonl")) &&
			!strings.HasPrefix(info.Name(), ".") {
			// 跳过分析结果文件和其它生成文件
			if strings.Contains(info.Name(), ".analysis") ||
				strings.Contains(info.Name(), "report") ||
				strings.Contains(info.Name(), "summary") {
				return nil
			}

			// 如果是.jsonl文件，使用LoadInputFile加载
			if strings.HasSuffix(info.Name(), ".jsonl") {
				batchResults, err := LoadInputFile(path)
				if err != nil {
					return nil
				}

				// 添加新的结果，避免重复
				for _, r := range batchResults {
					if !processedIDs[r.ID] {
						processedIDs[r.ID] = true
						results = append(results, r)
					}
				}
				return nil
			}

			// Process single JSON file
			result, err := LoadSingleResultFile(path)
			if err != nil {
				return nil
			}

			// 避免重复加载相同ID的记录
			if !processedIDs[result.ID] {
				processedIDs[result.ID] = true
				results = append(results, *result)
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("遍历目录失败: %v", err)
	}

	return results, nil
}

// EnsureDirectoryExists 确保目录存在
func EnsureDirectoryExists(dirPath string) error {
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return os.MkdirAll(dirPath, 0755)
	}
	return nil
}

// DetectDBType 根据目录名自动检测数据库类型
func DetectDBType(dirPath string) DBType {
	// 检查路径中是否包含PostgreSQL相关的关键字
	if strings.Contains(dirPath, "pg_") ||
		strings.Contains(dirPath, "_pg") ||
		strings.Contains(dirPath, "postgres") ||
		strings.Contains(dirPath, "postgresql") {
		return DBTypePostgreSQL
	}

	// 默认为SQLite
	return DBTypeSQLite
}

// ConvertResultFormat 将数据库结果转换为ExecResult格式
func ConvertResultFormat(data []map[string]interface{}) [][]string {
	if len(data) == 0 {
		return [][]string{}
	}

	// 提取列名作为第一行
	headers := make([]string, 0, len(data[0]))
	for k := range data[0] {
		headers = append(headers, k)
	}

	// 创建结果矩阵
	rows := make([][]string, 0, len(data)+1)
	rows = append(rows, headers) // 添加表头行

	// 添加数据行
	for _, row := range data {
		dataRow := make([]string, 0, len(headers))
		for _, h := range headers {
			val := fmt.Sprintf("%v", row[h])
			dataRow = append(dataRow, val)
		}
		rows = append(rows, dataRow)
	}

	return rows
}

// ConvertQueryResultFormat 将adapter.QueryResult转换为ExecResult格式
func ConvertQueryResultFormat(result *adapter.QueryResult) [][]string {
	if result == nil || len(result.Rows) == 0 {
		return [][]string{}
	}

	// 创建结果矩阵
	rows := make([][]string, 0, len(result.Rows)+1)
	rows = append(rows, result.Columns) // 添加表头行

	// 添加数据行
	for _, row := range result.Rows {
		dataRow := make([]string, 0, len(result.Columns))
		for _, col := range result.Columns {
			val := fmt.Sprintf("%v", row[col])
			dataRow = append(dataRow, val)
		}
		rows = append(rows, dataRow)
	}

	return rows
}
