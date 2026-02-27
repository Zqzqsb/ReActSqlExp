package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Reporter handles report generation and statistics
type Reporter struct {
	OutputDir string
}

// NewReporter creates a report generator
func NewReporter(outputDir string) *Reporter {
	return &Reporter{
		OutputDir: outputDir,
	}
}

// SaveAnalysisResult saves a single analysis result to file
func (r *Reporter) SaveAnalysisResult(result *AnalysisResult, originalFilePath string) error {
	analysisPath := originalFilePath + ".analysis"

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize analysis result: %v", err)
	}

	if err := os.WriteFile(analysisPath, resultJSON, 0644); err != nil {
		return fmt.Errorf("failed to write analysis result file: %v", err)
	}

	return nil
}

// GenerateSummaryReport generates a summary report
func (r *Reporter) GenerateSummaryReport(stats *ErrorStatistics, totalFiles int) error {
	reportDir := filepath.Join(r.OutputDir, "analysis_reports")
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		return fmt.Errorf("failed to create report directory: %v", err)
	}

	reportPath := filepath.Join(reportDir, "summary_report.json")

	// Calculate accuracy
	correctRate := float64(stats.CorrectCount+stats.EquivalentCount) / float64(totalFiles) * 100

	// Sort error counts by frequency
	sort.Slice(stats.ErrorCounts, func(i, j int) bool {
		return stats.ErrorCounts[i].Count > stats.ErrorCounts[j].Count
	})

	// Create report data
	report := map[string]interface{}{
		"total_files":      totalFiles,
		"correct_count":    stats.CorrectCount,
		"equivalent_count": stats.EquivalentCount,
		"ambiguous_count":  stats.AmbiguousCount,
		"error_count":      totalFiles - stats.CorrectCount - stats.EquivalentCount - stats.AmbiguousCount,
		"correct_rate":     correctRate,
		"error_statistics": map[string]interface{}{
			"syntax_error_count":     stats.SyntaxErrorCount,
			"projection_error_count": stats.ProjectionErrorCount,
			"data_error_count":       stats.DataErrorCount,
			"order_error_count":      stats.OrderErrorCount,
			"join_error_count":       stats.JoinErrorCount,
			"condition_error_count":  stats.ConditionErrorCount,
			"other_error_count":      stats.OtherErrorCount,
		},
		"error_counts": stats.ErrorCounts,
	}

	// Serialize report
	reportJSON, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize report: %v", err)
	}

	// Write report file
	if err := os.WriteFile(reportPath, reportJSON, 0644); err != nil {
		return fmt.Errorf("failed to write report file: %v", err)
	}

	return nil
}

// PrintSummary prints analysis results summary
func (r *Reporter) PrintSummary(stats *ErrorStatistics, totalFiles int) {
	// Calculate accuracy
	correctRate := float64(stats.CorrectCount+stats.EquivalentCount+stats.AmbiguousCount+stats.ReferenceErrorCount) / float64(totalFiles) * 100

	// Colored title
	fmt.Printf("\n%s%s======================================%s\n", Bold, ColorCyan, ColorReset)
	fmt.Printf("%s%s             SQL Analysis Summary             %s\n", Bold, ColorCyan, ColorReset)
	fmt.Printf("%s%s======================================%s\n\n", Bold, ColorCyan, ColorReset)

	// Basic statistics
	fmt.Printf("%sTotal Queries:%s %d\n", Bold, ColorReset, totalFiles)

	// Accuracy info, color based on percentage
	rateColor := ColorRed
	if correctRate >= 80 {
		rateColor = ColorGreen
	} else if correctRate >= 60 {
		rateColor = ColorYellow
	} else if correctRate >= 40 {
		rateColor = ColorBlue
	}

	fmt.Printf("%sCorrect Count:%s %s%d%s (Exact Match: %s%d%s, Semantic Match: %s%d%s)\n",
		Bold, ColorReset, ColorGreen, stats.CorrectCount+stats.EquivalentCount, ColorReset,
		ColorGreen, stats.CorrectCount, ColorReset,
		ColorGreen, stats.EquivalentCount, ColorReset)

	fmt.Printf("%sAmbiguous:%s %s%d%s\n", Bold, ColorReset, ColorYellow, stats.AmbiguousCount, ColorReset)

	errorCount := totalFiles - stats.CorrectCount - stats.EquivalentCount - stats.AmbiguousCount
	fmt.Printf("%sError Count:%s %s%d%s\n", Bold, ColorReset, ColorRed, errorCount, ColorReset)

	fmt.Printf("%sAccuracy (excl. ambiguous & ref errors):%s %s%.2f%%%s\n\n", Bold, ColorReset, rateColor, correctRate, ColorReset)

	// Error type statistics - sorted by frequency
	fmt.Printf("%s%sError Type Statistics (by frequency)%s\n", Bold, ColorRed, ColorReset)
	fmt.Printf("%s--------------------------------------%s\n", Bold, ColorReset)

	// Sort error counts by frequency
	sort.Slice(stats.ErrorCounts, func(i, j int) bool {
		return stats.ErrorCounts[i].Count > stats.ErrorCounts[j].Count
	})

	// Print error type statistics in table format
	fmt.Printf("%s%-20s %10s %15s%s\n", Bold, "Error Type", "Count", "Percentage", ColorReset)
	fmt.Printf("%s----------------------------------------%s\n", Bold, ColorReset)

	// Set color for each error type
	getErrorColor := func(errorType string) string {
		switch errorType {
		case "reference_error":
			return ColorYellow
		case "Projection Error":
			return ColorPurple
		case "Row Count Error":
			return ColorBlue
		case "data_mismatch":
			return ColorCyan
		case "ambiguous_query":
			return ColorYellow
		default:
			return ColorRed
		}
	}

	for _, ec := range stats.ErrorCounts {
		percentage := float64(ec.Count) / float64(totalFiles) * 100
		errorColor := getErrorColor(ec.Type)
		fmt.Printf("%-20s %s%10d%s %15.2f%%\n",
			ec.Type, errorColor, ec.Count, ColorReset, percentage)
	}

	// Detailed error statistics
	fmt.Printf("\n%s%sDetailed Error Classification%s\n", Bold, ColorBlue, ColorReset)
	fmt.Printf("%s--------------------------------------%s\n", Bold, ColorReset)

	// Print detailed error statistics
	fmt.Printf("%s%-20s %10s %15s%s\n", Bold, "Error Type", "Count", "Percentage", ColorReset)
	fmt.Printf("%s----------------------------------------%s\n", Bold, ColorReset)

	// Print function for each error type
	printErrorType := func(name string, count int, color string) {
		percent := float64(count) / float64(totalFiles) * 100
		fmt.Printf("%-20s %s%10d%s %15.2f%%\n", name, color, count, ColorReset, percent)
	}

	// Predicted SQL syntax errors
	printErrorType("Syntax Error", stats.SyntaxErrorCount, ColorYellow)
	// Reference answer syntax errors
	printErrorType("Reference Error", stats.ReferenceErrorCount, ColorYellow)
	// Projection error (column selection)
	printErrorType("Projection Error", stats.ProjectionErrorCount, ColorPurple)
	// Row count error
	printErrorType("Row Count Error", stats.RowErrorCount, ColorBlue)
	// Data mismatch error
	printErrorType("Data Mismatch", stats.DataErrorCount, ColorCyan)
	// Execution error
	printErrorType("Execution Error", stats.ExecutionErrorCount, ColorRed)
	// Other error
	printErrorType("Other Error", stats.OtherErrorCount, ColorRed)

	// SPJ statistics report
	if stats.SPJCaseCount > 0 {
		fmt.Printf("\n%s%sSPJ (Special Judge) Statistics%s\n", Bold, ColorPurple, ColorReset)
		fmt.Printf("%s--------------------------------------%s\n", Bold, ColorReset)

		spjCorrectRate := float64(stats.SPJCorrectCount) / float64(stats.SPJCaseCount) * 100
		spjColor := ColorRed
		if spjCorrectRate >= 80 {
			spjColor = ColorGreen
		} else if spjCorrectRate >= 60 {
			spjColor = ColorYellow
		}

		fmt.Printf("%sSPJ Total Cases:%s %s%d%s (of total queries %.2f%%)\n",
			Bold, ColorReset, ColorCyan, stats.SPJCaseCount, ColorReset,
			float64(stats.SPJCaseCount)/float64(totalFiles)*100)
		fmt.Printf("%sSPJ Correct:%s %s%d%s\n",
			Bold, ColorReset, ColorGreen, stats.SPJCorrectCount, ColorReset)
		fmt.Printf("%sSPJ Incorrect:%s %s%d%s\n",
			Bold, ColorReset, ColorRed, stats.SPJIncorrectCount, ColorReset)
		fmt.Printf("%sSPJ Accuracy:%s %s%.2f%%%s\n",
			Bold, ColorReset, spjColor, spjCorrectRate, ColorReset)

		fmt.Printf("\n%sNote:%s SPJ handles LIMIT 1 queries with multiple tied extreme values\n",
			Bold, ColorReset)
		fmt.Printf("      When Gold SQL uses LIMIT 1 but has multiple tied values, any correct value is accepted\n")
	}

	// Report save path
	reportPath := filepath.Join(r.OutputDir, "analysis_reports", "summary_report.json")
	fmt.Printf("\n%sReport saved to:%s %s%s%s\n",
		Bold, ColorReset, ColorGreen, reportPath, ColorReset)
}

// ResultClassifier classifies results by type and outputs to directories
type ResultClassifier struct {
	baseDir string
}

// NewResultClassifier creates a new result classifier
func NewResultClassifier(baseDir string) *ResultClassifier {
	return &ResultClassifier{
		baseDir: baseDir,
	}
}

// ClassifyAndSaveResults classifies and saves results
func (rc *ResultClassifier) ClassifyAndSaveResults(results []*AnalysisResult) error {
	// Create all needed directories
	directories := []string{
		"correct_exact_match",     // exact match correct
		"correct_equivalent",      // semantic match correct
		"incorrect_projection",    // Projection error
		"incorrect_row_count",     // Row count error
		"incorrect_data_mismatch", // Data mismatch error
		"incorrect_execution",     // execution error
		"incorrect_reference",     // reference error
		"incorrect_unknown",       // unknown error
		"ambiguous_queries",       // ambiguous queries
	}

	for _, dir := range directories {
		fullPath := filepath.Join(rc.baseDir, dir)
		if err := EnsureDirectoryExists(fullPath); err != nil {
			return fmt.Errorf("failed to create directory %s: %v", fullPath, err)
		}
	}

	// Classify and save by type
	for _, result := range results {
		var category string

		if result.IsCorrect {
			// Determine exact or semantic match
			if result.ErrorType == "exact_match" {
				category = "correct_exact_match"
			} else if result.ErrorType == "semantic_match" {
				category = "correct_equivalent"
			} else {
				// Backward compat, default exact match
				category = "correct_exact_match"
			}
		} else if result.ErrorType == "ambiguous_query" {
			category = "ambiguous_queries"
		} else {
			// Classify by error type
			switch result.ErrorType {
			case "projection_error", "Projection Error":
				category = "incorrect_projection"
			case "row_count_error", "Row Count Error":
				category = "incorrect_row_count"
			case "data_mismatch":
				category = "incorrect_data_mismatch"
			case "execution_error", "Execution Error":
				category = "incorrect_execution"
			case "reference_error":
				category = "incorrect_reference"
			default:
				category = "incorrect_unknown"
			}
		}

		// Save result to directory
		filename := fmt.Sprintf("%s_%d.json", strings.ToLower(category), result.ID)
		filePath := filepath.Join(rc.baseDir, category, filename)

		if err := rc.saveResultToFile(result, filePath); err != nil {
			fmt.Printf("failed to save result %d to %s: %v\n", result.ID, filePath, err)
			continue
		}
	}

	return nil
}

// saveResultToFile saves a single analysis result to JSON
func (rc *ResultClassifier) saveResultToFile(result *AnalysisResult, filePath string) error {
	// Extract error info
	var gtError, predError interface{}
	if result.GTResult != nil && !result.GTResult.Success {
		gtError = result.GTResult.Error
	}
	if result.PredResult != nil && !result.PredResult.Success {
		predError = result.PredResult.Error
	}

	// Convert to expected JSON format
	outputData := map[string]interface{}{
		"id":            result.ID,
		"db_id":         result.DBName,
		"question":      result.Question,
		"thinking":      result.Thinking,
		"gt_sql":        result.GTSQL,
		"pred_sql":      result.PredSQL,
		"is_correct":    result.IsCorrect,
		"is_equivalent": result.IsEquivalent,
		"error_reason":  result.ErrorReason,
		"gt_result":     result.GTResult,
		"pred_result":   result.PredResult,
		"gt_error":      gtError,
		"pred_error":    predError,
	}

	// Encode to JSON
	jsonData, err := json.MarshalIndent(outputData, "", "  ")
	if err != nil {
		return fmt.Errorf("JSON encoding failed: %v", err)
	}

	// Write file
	return os.WriteFile(filePath, jsonData, 0644)
}
