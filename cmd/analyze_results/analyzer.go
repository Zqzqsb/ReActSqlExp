package main

import (
	"fmt"
	"sort"
	"strings"
)

// Color constants
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorPurple = "\033[35m"
	ColorCyan   = "\033[36m"
	ColorWhite  = "\033[37m"
	Bold        = "\033[1m"
)

// SQLAnalyzer handles SQL analysis operations
type SQLAnalyzer struct {
	Stats *ErrorStatistics
}

// NewSQLAnalyzer creates a new SQL analyzer
func NewSQLAnalyzer() *SQLAnalyzer {
	return &SQLAnalyzer{
		Stats: &ErrorStatistics{
			ErrorCounts: make([]ErrorCount, 0),
		},
	}
}

// AnalyzeSQL analyzes a single SQL query
// Uses pre-executed results for analysis
func (a *SQLAnalyzer) AnalyzeSQL(input InputResult, gtResult, predResult *ExecResult, gtErr, predErr error) *AnalysisResult {
	result := &AnalysisResult{
		ID:           input.ID,
		DBName:       input.DBName,
		Question:     input.Question,
		GTSQL:        input.GTSQL,
		PredSQL:      input.PredSQL,
		Thinking:     input.Thinking,
		Ambiguous:    input.Ambiguous,
		IsCorrect:    false,
		IsEquivalent: false,
	}

	// Check for ambiguous query
	if input.PredSQL == "AMBIGUOUS_QUERY" {
		result.ErrorReason = "ambiguous query needs clarification"
		result.ErrorType = "ambiguous_query"
		a.Stats.AmbiguousCount++
		return result
	}

	// Check if predicted SQL is empty
	if input.PredSQL == "" {
		result.ErrorReason = "predicted SQL is empty"
		result.ErrorType = "execution_error"
		a.Stats.SyntaxErrorCount++
		return result
	}

	// 1. Check if normalized SQL matches exactly
	// Includes lowercasing, removing semicolons and normalizing whitespace
	if NormalizeSQL(input.PredSQL) == NormalizeSQL(input.GTSQL) {
		result.IsCorrect = true
		result.IsEquivalent = true
		result.ErrorReason = ""
		result.ErrorType = "exact_match"
		a.Stats.CorrectCount++
		return result
	}

	// Handle execution errors
	if gtErr != nil {
		errorStr := gtErr.Error()
		if strings.Contains(errorStr, "connection") ||
			strings.Contains(errorStr, "database") {
			result.ErrorReason = fmt.Sprintf("database connection error: %v", gtErr)
			result.ErrorType = "db_connection_error"
			a.Stats.DBNotExistCount++
			return result
		} else {
			result.ErrorReason = fmt.Sprintf("gold SQL execution error: %v", gtErr)
			result.ErrorType = "reference_error"
			a.Stats.ReferenceErrorCount++
			return result
		}
	}

	if predErr != nil {
		errorStr := predErr.Error()
		if strings.Contains(errorStr, "connection") ||
			strings.Contains(errorStr, "database") {
			result.ErrorReason = fmt.Sprintf("database connection error: %v", predErr)
			result.ErrorType = "db_connection_error"
			a.Stats.DBNotExistCount++
			return result
		} else {
			result.ErrorReason = fmt.Sprintf("predicted SQL execution error: %v", predErr)
			result.ErrorType = "execution_error"
			a.Stats.ExecutionErrorCount++
			return result
		}
	}

	// Both SQL executed successfully, check result equivalence

	// Check result equivalence
	isEquiv, errorReason := a.areResultsEquivalent(gtResult, predResult)

	// Save execution results
	result.GTResult = gtResult
	result.PredResult = predResult
	result.SPJType = input.SPJType

	// If equivalent, mark as correct
	if isEquiv {
		result.IsCorrect = true
		result.IsEquivalent = true
		result.ErrorReason = ""
		result.ErrorType = "semantic_match"
		a.Stats.EquivalentCount++
		return result
	}

	// If not equivalent but has SPJ tag, try SPJ judgment
	if input.SPJType != "" && input.SPJType != "null" {
		spjCorrect, spjReason := a.applySPJ(input.SPJType, input.GTSQL, input.PredSQL, gtResult, predResult)
		result.SPJResult = spjReason

		if spjCorrect {
			result.IsCorrect = true
			result.IsEquivalent = true
			result.ErrorReason = ""
			result.ErrorType = "spj_correct"
			a.Stats.EquivalentCount++
			a.Stats.SPJCaseCount++
			a.Stats.SPJCorrectCount++
			return result
		} else {
			// SPJ judged as incorrect
			a.Stats.SPJCaseCount++
			a.Stats.SPJIncorrectCount++
			// Continue with normal error classification
		}
	}

	// Set error reason
	result.ErrorReason = errorReason

	// Classify error type
	errorType := a.classifyError(errorReason)
	result.ErrorType = errorType

	// Update statistics
	a.updateErrorStats(errorType)

	return result
}

// classifyError classifies error type based on error reason
// Following priority-ordered error classification
func (a *SQLAnalyzer) classifyError(errorReason string) string {
	// Priority 1 (highest): Unknown Error
	if errorReason == "" {
		return "unknown_error"
	}

	// Priority 2: Reference Answer Syntax Error
	if strings.Contains(strings.ToLower(errorReason), "gold sql execution failed") ||
		strings.Contains(strings.ToLower(errorReason), "gold sql execution error") {
		return "reference_error"
	}

	// Priority 3: Execution Error
	if strings.Contains(strings.ToLower(errorReason), "predicted sql execution failed") ||
		strings.Contains(strings.ToLower(errorReason), "predicted sql execution error") ||
		strings.Contains(strings.ToLower(errorReason), "syntax error") {
		return "execution_error"
	}

	// Priority 4: Row Count Error
	if strings.Contains(strings.ToLower(errorReason), "row count mismatch") ||
		strings.Contains(strings.ToLower(errorReason), "data row count") {
		return "row_count_error"
	}

	// Priority 5: Data Mismatch Error
	errorReasonLower := strings.ToLower(errorReason)
	hasColumnMismatch := strings.Contains(errorReasonLower, "column name mismatch")
	hasDataMismatch := strings.Contains(errorReasonLower, "data mismatch") ||
		strings.Contains(errorReasonLower, "data inconsistency") ||
		strings.Contains(errorReasonLower, "value mismatch")

	if hasColumnMismatch && hasDataMismatch {
		return "data_mismatch"
	}

	// Priority 6: Projection Error
	if strings.Contains(errorReasonLower, "column count mismatch") ||
		strings.Contains(errorReasonLower, "column name mismatch") ||
		strings.Contains(errorReasonLower, "column name count") {
		return "projection_error"
	}

	// Data mismatch (lower priority)
	if hasDataMismatch {
		return "data_mismatch"
	}

	// Order error
	if strings.Contains(strings.ToLower(errorReason), "order") {
		return "order_error"
	}

	// Join error
	if strings.Contains(strings.ToLower(errorReason), "join") {
		return "join_error"
	}

	// Condition error
	if strings.Contains(strings.ToLower(errorReason), "where") ||
		strings.Contains(strings.ToLower(errorReason), "condition") {
		return "condition_error"
	}

	// No known pattern matched
	return "other_error"
}

// updateErrorStats updates error statistics
func (a *SQLAnalyzer) updateErrorStats(errorType string) {
	// Update error count
	found := false
	for i, ec := range a.Stats.ErrorCounts {
		if ec.Type == errorType {
			a.Stats.ErrorCounts[i].Count++
			found = true
			break
		}
	}

	if !found {
		a.Stats.ErrorCounts = append(a.Stats.ErrorCounts, ErrorCount{
			Type:  errorType,
			Count: 1,
		})
	}

	// Update error type statistics
	switch errorType {
	case "reference_error":
		a.Stats.ReferenceErrorCount++
	case "execution_error":
		a.Stats.ExecutionErrorCount++
	case "db_connection_error":
		a.Stats.DBNotExistCount++
	case "row_count_error":
		a.Stats.RowErrorCount++
	case "projection_error":
		a.Stats.ProjectionErrorCount++
	case "data_mismatch":
		a.Stats.DataErrorCount++
	default:
		a.Stats.OtherErrorCount++
	}
}

// GetStatistics returns error statistics
func (a *SQLAnalyzer) GetStatistics() *ErrorStatistics {
	return a.Stats
}

// MergeStats merges another analyzer's stats into this one
func (a *SQLAnalyzer) MergeStats(other *ErrorStatistics) {
	a.Stats.CorrectCount += other.CorrectCount
	a.Stats.EquivalentCount += other.EquivalentCount
	a.Stats.ExecutionErrorCount += other.ExecutionErrorCount
	a.Stats.ReferenceErrorCount += other.ReferenceErrorCount
	a.Stats.SyntaxErrorCount += other.SyntaxErrorCount
	a.Stats.DBNotExistCount += other.DBNotExistCount
	a.Stats.RowErrorCount += other.RowErrorCount
	a.Stats.ProjectionErrorCount += other.ProjectionErrorCount
	a.Stats.DataErrorCount += other.DataErrorCount
	a.Stats.OtherErrorCount += other.OtherErrorCount
	a.Stats.AmbiguousCount += other.AmbiguousCount
	a.Stats.SPJCaseCount += other.SPJCaseCount
	a.Stats.SPJCorrectCount += other.SPJCorrectCount
	a.Stats.SPJIncorrectCount += other.SPJIncorrectCount

	for _, ec := range other.ErrorCounts {
		found := false
		for i, existing := range a.Stats.ErrorCounts {
			if existing.Type == ec.Type {
				a.Stats.ErrorCounts[i].Count += ec.Count
				found = true
				break
			}
		}
		if !found {
			a.Stats.ErrorCounts = append(a.Stats.ErrorCounts, ec)
		}
	}
}

// NormalizeSQL normalizes SQL query for comparison
func NormalizeSQL(sql string) string {
	sql = strings.ToLower(sql)
	sql = strings.TrimSuffix(sql, ";")
	sql = strings.Join(strings.Fields(sql), " ")
	return sql
}

// minInt returns the minimum of multiple integers
func minInt(values ...int) int {
	if len(values) == 0 {
		return 0
	}
	min := values[0]
	for _, v := range values[1:] {
		if v < min {
			min = v
		}
	}
	return min
}

// areResultsEquivalent checks if two execution results are equivalent
func (a *SQLAnalyzer) areResultsEquivalent(result1, result2 *ExecResult) (bool, string) {
	if !result1.Success || !result2.Success {
		if !result1.Success {
			return false, "gold SQL execution failed: " + result1.Error
		}
		return false, "predicted SQL execution failed: " + result2.Error
	}

	// If no data, consider equivalent
	if len(result1.Rows) <= 1 || len(result2.Rows) <= 1 {
		if len(result1.Rows) <= 1 && len(result2.Rows) <= 1 {
			return true, ""
		}
		return false, fmt.Sprintf("row count mismatch: gold=%d, pred=%d",
			len(result1.Rows)-1, len(result2.Rows)-1)
	}

	// Step 1: Get column name to index mapping
	headers1 := result1.Rows[0]
	headers2 := result2.Rows[0]

	headerToIndex1 := make(map[string]int)
	headerToIndex2 := make(map[string]int)

	for i, h := range headers1 {
		headerToIndex1[strings.ToLower(h)] = i
	}

	for i, h := range headers2 {
		headerToIndex2[strings.ToLower(h)] = i
	}

	// Step 2: Check row count
	dataRows1 := len(result1.Rows) - 1
	dataRows2 := len(result2.Rows) - 1

	if dataRows1 != dataRows2 {
		return false, fmt.Sprintf("row count mismatch: gold=%d, pred=%d",
			dataRows1, dataRows2)
	}

	// Step 3: Check column count
	if len(headerToIndex1) != len(headerToIndex2) {
		return false, fmt.Sprintf("column count mismatch: gold=%d, pred=%d",
			len(headerToIndex1), len(headerToIndex2))
	}

	// Step 4: Try multiple matching strategies
	var convertedRows1, convertedRows2 [][]string
	var matchingStrategy string

	// Strategy 1: Exact column name match (ignoring order)
	columnsExactMatch := true
	for header := range headerToIndex1 {
		if _, exists := headerToIndex2[header]; !exists {
			columnsExactMatch = false
			break
		}
	}

	if columnsExactMatch {
		matchingStrategy = "exact_column_names"
		// Get unified column order (alphabetical)
		sortedColumns := make([]string, 0, len(headerToIndex1))
		for header := range headerToIndex1 {
			sortedColumns = append(sortedColumns, header)
		}
		sort.Strings(sortedColumns)

		// Convert result sets to comparable format
		convertedRows1 = make([][]string, dataRows1)
		convertedRows2 = make([][]string, dataRows2)

		// Convert gold SQL results to unified column order
		for i := 1; i <= dataRows1; i++ {
			row := make([]string, len(sortedColumns))
			for j, colName := range sortedColumns {
				colIndex := headerToIndex1[colName]
				if colIndex < len(result1.Rows[i]) { // prevent index out of bounds
					row[j] = result1.Rows[i][colIndex]
				} else {
					row[j] = ""
				}
			}
			convertedRows1[i-1] = row
		}

		// Convert predicted SQL results to unified column order
		for i := 1; i <= dataRows2; i++ {
			row := make([]string, len(sortedColumns))
			for j, colName := range sortedColumns {
				colIndex := headerToIndex2[colName]
				if colIndex < len(result2.Rows[i]) { // prevent index out of bounds
					row[j] = result2.Rows[i][colIndex]
				} else {
					row[j] = ""
				}
			}
			convertedRows2[i-1] = row
		}
	} else {
		// Strategy 2: Smart column reordering based on content feature matching
		matchingStrategy = "content_based_mapping"
		convertedRows1 = make([][]string, dataRows1)
		convertedRows2 = make([][]string, dataRows2)

		// Extract gold SQL data rows
		for i := 1; i <= dataRows1; i++ {
			row := make([]string, len(headers1))
			for j := 0; j < len(headers1); j++ {
				if j < len(result1.Rows[i]) {
					row[j] = result1.Rows[i][j]
				} else {
					row[j] = ""
				}
			}
			convertedRows1[i-1] = row
		}

		// Smart column reordering: based on column feature values
		mapping := findColumnMapping(result1, result2)

		if mapping != nil {
			// Found valid column mapping, reorder predicted results
			for i := 1; i <= dataRows2; i++ {
				row := make([]string, len(headers1))
				for j := 0; j < len(headers1); j++ {
					srcCol := mapping[j]
					if srcCol < len(result2.Rows[i]) {
						row[j] = result2.Rows[i][srcCol]
					} else {
						row[j] = ""
					}
				}
				convertedRows2[i-1] = row
			}
		} else {
			// Strategy 3: Positional comparison (ignore column names)
			matchingStrategy = "positional_comparison"
			for i := 1; i <= dataRows2; i++ {
				row := make([]string, len(headers2))
				for j := 0; j < len(headers2) && j < len(headers1); j++ {
					if j < len(result2.Rows[i]) {
						row[j] = result2.Rows[i][j]
					} else {
						row[j] = ""
					}
				}
				convertedRows2[i-1] = row
			}
		}
	}

	// Step 5: Compare data content (order-independent, preserving duplicates)
	// Use map[string]int to count occurrences (not map[string]bool which deduplicates)
	rowCounts1 := make(map[string]int)
	rowCounts2 := make(map[string]int)

	for _, row := range convertedRows1 {
		rowStr := strings.Join(row, "|")
		rowCounts1[rowStr]++
	}

	for _, row := range convertedRows2 {
		rowStr := strings.Join(row, "|")
		rowCounts2[rowStr]++
	}

	// Check if unique row counts match
	if len(rowCounts1) != len(rowCounts2) {
		return false, fmt.Sprintf("data row count mismatch (strategy: %s)", matchingStrategy)
	}

	// Check if each row exists in the other result set with the same count
	for rowStr, count1 := range rowCounts1 {
		count2, exists := rowCounts2[rowStr]
		if exists && count1 == count2 {
			continue
		}
		// Exact match failed, try loose matching (considering time types)
		found := false
		row1 := strings.Split(rowStr, "|")

		for rowStr2, cnt2 := range rowCounts2 {
			if cnt2 != count1 {
				continue // multiplicity must match
			}
			row2 := strings.Split(rowStr2, "|")

			if len(row1) != len(row2) {
				continue
			}

			allMatch := true
			for i := 0; i < len(row1); i++ {
				if !areValuesEquivalent(row1[i], row2[i]) {
					allMatch = false
					break
				}
			}

			if allMatch {
				found = true
				break
			}
		}

		if !found {
			switch matchingStrategy {
			case "exact_column_names":
				return false, "data mismatch"
			case "content_based_mapping":
				return false, "column name mismatch and data mapping failed"
			case "positional_comparison":
				return false, "column name mismatch and positional data mismatch"
			default:
				return false, "data mismatch"
			}
		}
	}

	// Passed all checks, consider equivalent
	return true, ""
}

// findColumnMapping finds column mapping based on column features
// Returns mapping array: mapping[i] = j means gold column i maps to pred column j
func findColumnMapping(result1, result2 *ExecResult) []int {
	if len(result1.Rows) <= 1 || len(result2.Rows) <= 1 {
		return nil
	}

	headers1 := result1.Rows[0]
	headers2 := result2.Rows[0]

	if len(headers1) != len(headers2) {
		return nil
	}

	colCount := len(headers1)

	// Calculate feature values for each column (based on multi-row data hash)
	features1 := make([]string, colCount)
	features2 := make([]string, colCount)

	// Use more rows for better matching accuracy
	maxRows := minInt(11, len(result1.Rows), len(result2.Rows)) // includes header row, so actually first 10 data rows

	for col := 0; col < colCount; col++ {
		// Calculate feature for gold SQL column col
		var vals1 []string
		for row := 1; row < maxRows; row++ {
			if col < len(result1.Rows[row]) {
				vals1 = append(vals1, strings.TrimSpace(result1.Rows[row][col]))
			}
		}
		features1[col] = strings.Join(vals1, ":")

		// Calculate feature for predicted SQL column col
		var vals2 []string
		for row := 1; row < maxRows; row++ {
			if col < len(result2.Rows[row]) {
				vals2 = append(vals2, strings.TrimSpace(result2.Rows[row][col]))
			}
		}
		features2[col] = strings.Join(vals2, ":")
	}

	// Try to find best match
	mapping := make([]int, colCount)
	used := make([]bool, colCount)

		// Find best matching predicted column for each gold column
	for i := 0; i < colCount; i++ {
		bestMatch := -1
		bestScore := 0.0

		for j := 0; j < colCount; j++ {
			if used[j] {
				continue
			}

			// Calculate feature similarity
			score := calculateFeatureSimilarity(features1[i], features2[j])

			// If exact match, select directly
			if score >= 1.0 {
				bestMatch = j
				break
			}

			// If similarity high enough, record as candidate
			if score > 0.8 && score > bestScore {
				bestMatch = j
				bestScore = score
			}
		}

		if bestMatch == -1 {
			// No suitable matching column found
			return nil
		}

		mapping[i] = bestMatch
		used[bestMatch] = true
	}

	return mapping
}

// calculateFeatureSimilarity calculates similarity between two feature strings
func calculateFeatureSimilarity(feature1, feature2 string) float64 {
	if feature1 == feature2 {
		return 1.0
	}

	// If either is empty, similarity is 0
	if feature1 == "" || feature2 == "" {
		return 0.0
	}

	// Split feature strings into value arrays
	vals1 := strings.Split(feature1, ":")
	vals2 := strings.Split(feature2, ":")

	// If different lengths, low similarity
	if len(vals1) != len(vals2) {
		return 0.0
	}

	// Count matching values
	matchCount := 0
	for i := 0; i < len(vals1) && i < len(vals2); i++ {
		if vals1[i] == vals2[i] {
			matchCount++
		}
	}

	// Return match ratio
	return float64(matchCount) / float64(len(vals1))
}

// isTimeValue checks if a string is a time value
func isTimeValue(s string) bool {
	// Check common time format characteristics
	return strings.Contains(s, "-") && (strings.Contains(s, ":") || strings.Contains(s, "UTC") || strings.Contains(s, "+0000"))
}

// normalizeTimeValue normalizes time values, removing high-precision parts for comparison
func normalizeTimeValue(s string) string {
	// Remove UTC timezone info and milliseconds/microseconds
	s = strings.TrimSpace(s)

	// Remove " +0000 UTC" or similar timezone suffixes
	if idx := strings.Index(s, " +"); idx != -1 {
		s = s[:idx]
	}
	if idx := strings.Index(s, " UTC"); idx != -1 {
		s = s[:idx]
	}

	// Remove milliseconds if present
	if idx := strings.LastIndex(s, "."); idx != -1 {
		// Check if digits after decimal (milliseconds/microseconds)
		after := s[idx+1:]
		if len(after) > 0 && after[0] >= '0' && after[0] <= '9' {
			s = s[:idx]
		}
	}

	return s
}

// areValuesEquivalent checks if two values are equivalent (with loose time comparison)
func areValuesEquivalent(val1, val2 string) bool {
	// Exact match
	if val1 == val2 {
		return true
	}

	// Check if both are time values
	if isTimeValue(val1) && isTimeValue(val2) {
		// Compare after normalization
		norm1 := normalizeTimeValue(val1)
		norm2 := normalizeTimeValue(val2)
		return norm1 == norm2
	}

	// Check if percentage values (ignore % symbol)
	norm1 := strings.TrimSpace(val1)
	norm2 := strings.TrimSpace(val2)

	// Compare after removing percent sign
	norm1 = strings.TrimSuffix(norm1, "%")
	norm2 = strings.TrimSuffix(norm2, "%")

	if norm1 == norm2 {
		return true
	}

	return false
}
