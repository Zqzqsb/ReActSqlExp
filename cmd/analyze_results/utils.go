package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"reactsql/internal/adapter"
)

// LoadInputFile loads input results from file
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

	// Set larger buffer for long JSON lines (10MB)
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

// LoadSingleResultFile loads results from a single JSON file
func LoadSingleResultFile(filePath string) (*InputResult, error) {
	// Read file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %v", err)
	}

	// Parse JSON
	var result InputResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %v", err)
	}

	return &result, nil
}

// LoadResultsFromDirectory loads all results from directory
func LoadResultsFromDirectory(dirPath string) ([]InputResult, error) {
	// Check if directory exists
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("directory not found: %s", dirPath)
	}

	// First check for info.jsonl file to load directly
	jsonlPath := filepath.Join(dirPath, "info.jsonl")
	if _, err := os.Stat(jsonlPath); err == nil {
		return LoadInputFile(jsonlPath)
	}

	var results []InputResult
	// Use set to track processed files, avoid duplicates
	processedIDs := make(map[int]bool)

	// Walk all JSON files in directory
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Only process JSON files, skip hidden files
		if !info.IsDir() &&
			(strings.HasSuffix(info.Name(), ".json") || strings.HasSuffix(info.Name(), ".jsonl")) &&
			!strings.HasPrefix(info.Name(), ".") {
			// Skip analysis and generated files
			if strings.Contains(info.Name(), ".analysis") ||
				strings.Contains(info.Name(), "report") ||
				strings.Contains(info.Name(), "summary") {
				return nil
			}

			// For .jsonl files, use LoadInputFile
			if strings.HasSuffix(info.Name(), ".jsonl") {
				batchResults, err := LoadInputFile(path)
				if err != nil {
					return nil
				}

				// Add new results, avoid duplicates
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

			// Avoid duplicate ID loading
			if !processedIDs[result.ID] {
				processedIDs[result.ID] = true
				results = append(results, *result)
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk directory: %v", err)
	}

	return results, nil
}

// EnsureDirectoryExists ensures directory exists
func EnsureDirectoryExists(dirPath string) error {
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return os.MkdirAll(dirPath, 0755)
	}
	return nil
}

// DetectDBType auto-detects DB type from directory name
func DetectDBType(dirPath string) DBType {
	// Check if path contains PostgreSQL keywords
	if strings.Contains(dirPath, "pg_") ||
		strings.Contains(dirPath, "_pg") ||
		strings.Contains(dirPath, "postgres") ||
		strings.Contains(dirPath, "postgresql") {
		return DBTypePostgreSQL
	}

	// Default to SQLite
	return DBTypeSQLite
}

// ConvertResultFormat converts DB result to ExecResult format
func ConvertResultFormat(data []map[string]interface{}) [][]string {
	if len(data) == 0 {
		return [][]string{}
	}

	// Extract column names as first row
	headers := make([]string, 0, len(data[0]))
	for k := range data[0] {
		headers = append(headers, k)
	}

	// Create result matrix
	rows := make([][]string, 0, len(data)+1)
	rows = append(rows, headers) // Add header row

	// Add data rows
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

// ConvertQueryResultFormat converts adapter.QueryResult to ExecResult format
func ConvertQueryResultFormat(result *adapter.QueryResult) [][]string {
	if result == nil || len(result.Rows) == 0 {
		return [][]string{}
	}

	colCount := len(result.Columns)
	rows := make([][]string, 0, len(result.Rows)+1)
	rows = append(rows, result.Columns) // Add header row

	// Add data rows — avoid fmt.Sprintf for common types
	for _, row := range result.Rows {
		dataRow := make([]string, colCount)
		for j, col := range result.Columns {
			val := row[col]
			switch v := val.(type) {
			case string:
				dataRow[j] = v
			case []byte:
				dataRow[j] = string(v)
			case int64:
				dataRow[j] = strconv.FormatInt(v, 10)
			case float64:
				dataRow[j] = strconv.FormatFloat(v, 'f', -1, 64)
			case bool:
				if v {
					dataRow[j] = "true"
				} else {
					dataRow[j] = "false"
				}
			case nil:
				dataRow[j] = "<nil>"
			default:
				dataRow[j] = fmt.Sprintf("%v", v)
			}
		}
		rows = append(rows, dataRow)
	}

	return rows
}
