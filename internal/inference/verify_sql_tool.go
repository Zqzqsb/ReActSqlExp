package inference

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"reactsql/internal/adapter"
)

// VerifySQLTool SQL syntax verification tool
type VerifySQLTool struct {
	adapter adapter.DBAdapter
	dbType  string
	logger  *InferenceLogger
}

// Name returns tool name
func (t *VerifySQLTool) Name() string {
	return "verify_sql"
}

// Description returns tool description
func (t *VerifySQLTool) Description() string {
	return `Verify SQL syntax AND result reasonableness before submitting final answer.
This tool checks for common syntax errors, validates via database execution, and reports result quality.

Input: SQL query string to verify
Output: Verification report with syntax check, row count, sample results, and warnings

Use this tool BEFORE giving your final answer to ensure SQL correctness.`
}

// Call executes verification
func (t *VerifySQLTool) Call(ctx context.Context, input string) (string, error) {
	sql := strings.TrimSpace(input)

	logf := func(format string, a ...interface{}) {
		if t.logger != nil {
			t.logger.Printf(format, a...)
		} else {
			fmt.Printf(format, a...)
		}
	}

	logf("\n🔍 Tool Call [verify_sql]:\n")
	logf("Input SQL: %s\n", sql)

	// 1. Quick static check (avoid obvious errors)
	if err := t.quickCheck(sql); err != nil {
		result := fmt.Sprintf("❌ SQL validation failed (static check):\n%v\n\nPlease fix the error and try again.", err)
		logf("Output: %s\n", result)
		return result, nil
	}

	// 2. Execute SQL for validation and result analysis
	data, err := t.adapter.ExecuteQuery(ctx, sql)
	if err != nil {
		result := fmt.Sprintf("❌ SQL validation failed (database check):\n%v\n\nPlease fix the error and try again.", err)
		logf("Output: %s\n", result)
		return result, nil
	}

	var report strings.Builder
	report.WriteString("✓ SQL is valid!\n")

	// 3. Row count analysis
	report.WriteString(fmt.Sprintf("Row count: %d\n", data.RowCount))

	var warnings []string

	if data.RowCount == 0 {
		warnings = append(warnings, "⚠️  Query returned 0 rows. Check:\n  - Are JOIN conditions correct?\n  - Are WHERE conditions too restrictive?\n  - Does the data exist? Try relaxing conditions.")
	}

	// 4. Sample results (first 3 rows)
	if data.RowCount > 0 && len(data.Rows) > 0 {
		report.WriteString("Sample results:\n")
		maxShow := 3
		if data.RowCount < 3 {
			maxShow = data.RowCount
		}
		for i := 0; i < maxShow && i < len(data.Rows); i++ {
			report.WriteString(fmt.Sprintf("  Row %d: %v\n", i+1, data.Rows[i]))
		}

		// 5. Check for NULL values in results
		hasNull := false
		for _, row := range data.Rows {
			for _, val := range row {
				if val == nil {
					hasNull = true
					break
				}
			}
			if hasNull {
				break
			}
		}
		if hasNull {
			warnings = append(warnings, "⚠️  Results contain NULL values. Consider adding IS NOT NULL filters if NULLs are unexpected.")
		}
	}

	// 6. Check duplicate rows
	rows := convertQueryResultFormat(data.Rows)
	if duplicateWarning := t.checkDuplicateRows(rows); duplicateWarning != "" {
		warnings = append(warnings, duplicateWarning)
	}

	// 7. Build final result
	if len(warnings) > 0 {
		report.WriteString(strings.Join(warnings, "\n"))
		report.WriteString("\n")
	}

	report.WriteString("If results look correct, proceed to Final Answer.")

	result := report.String()
	logf("Output: %s\n", result)
	return result, nil
}

// quickCheck quick static check
func (t *VerifySQLTool) quickCheck(sql string) error {
	// 1. Check illegal aliases (most common)
	if err := t.checkIllegalAliases(sql); err != nil {
		return err
	}

	// 2. Check parentheses matching
	if err := t.checkParentheses(sql); err != nil {
		return err
	}

	return nil
}

// checkIllegalAliases checks illegal aliases
func (t *VerifySQLTool) checkIllegalAliases(sql string) error {
	// Match AS followed by function-call aliases
	// e.g.: AS count(*), AS sum(*), AS max(*) etc.
	illegalAliasPattern := regexp.MustCompile(`(?i)\s+AS\s+([a-z_]+\s*\([^)]*\))`)

	matches := illegalAliasPattern.FindAllStringSubmatch(sql, -1)
	if len(matches) > 0 {
		aliases := make([]string, 0, len(matches))
		for _, match := range matches {
			if len(match) > 1 {
				aliases = append(aliases, match[1])
			}
		}
		return fmt.Errorf("illegal alias syntax: %v\nAliases cannot contain parentheses.\nUse simple names like 'total_count' instead of 'count(*)'", aliases)
	}

	return nil
}

// checkParentheses checks parentheses matching
func (t *VerifySQLTool) checkParentheses(sql string) error {
	stack := 0
	for i, char := range sql {
		if char == '(' {
			stack++
		} else if char == ')' {
			stack--
			if stack < 0 {
				return fmt.Errorf("unmatched closing parenthesis at position %d", i)
			}
		}
	}

	if stack > 0 {
		return fmt.Errorf("unmatched opening parenthesis: %d unclosed", stack)
	}

	return nil
}

// NewVerifySQLTool creates verification tool
func NewVerifySQLTool(adapter adapter.DBAdapter, dbType string) *VerifySQLTool {
	return &VerifySQLTool{
		adapter: adapter,
		dbType:  dbType,
	}
}

// checkDuplicateRows checks for duplicate rows
func (t *VerifySQLTool) checkDuplicateRows(rows [][]string) string {
	if len(rows) <= 2 { // no data rows or only one row
		return ""
	}

	seen := make(map[string]bool)
	dataRows := rows[1:] // Exclude header row

	for _, row := range dataRows {
		// Create unique key for row
		rowKey := strings.Join(row, "||<SEP>||")
		if seen[rowKey] {
			// Duplicate found
			return fmt.Sprintf("Warning: The query returned duplicate rows (e.g., %v). Review the question to determine if duplicates should be removed using DISTINCT.", row)
		}
		seen[rowKey] = true
	}

	return ""
}

// convertQueryResultFormat converts query result from map to 2D string array
func convertQueryResultFormat(data []map[string]interface{}) [][]string {
	if len(data) == 0 {
		return nil
	}

	var headers []string
	for key := range data[0] {
		headers = append(headers, key)
	}

	result := make([][]string, len(data)+1)
	result[0] = headers

	for i, row := range data {
		rowValues := make([]string, len(headers))
		for j, header := range headers {
			if val, ok := row[header]; ok {
				rowValues[j] = fmt.Sprintf("%v", val)
			} else {
				rowValues[j] = ""
			}
		}
		result[i+1] = rowValues
	}

	return result
}
