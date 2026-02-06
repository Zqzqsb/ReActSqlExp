package inference

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"reactsql/internal/adapter"
)

// VerifySQLTool SQL 语法验证工具
type VerifySQLTool struct {
	adapter adapter.DBAdapter
	dbType  string
}

// Name 工具名称
func (t *VerifySQLTool) Name() string {
	return "verify_sql"
}

// Description 工具描述
func (t *VerifySQLTool) Description() string {
	return `Verify SQL syntax before submitting final answer.
This tool checks for common syntax errors and validates the SQL using database dry-run.

Input: SQL query string to verify
Output: "✓ SQL is valid" or error message with suggestions

Common errors detected:
- Illegal aliases like "AS count(*)" or "AS sum(*)"
- Unmatched parentheses
- Basic syntax errors

Use this tool BEFORE giving your final answer to ensure SQL correctness.`
}

// Call 执行验证
func (t *VerifySQLTool) Call(ctx context.Context, input string) (string, error) {
	sql := strings.TrimSpace(input)

	fmt.Printf("\n🔍 Tool Call [verify_sql]:\n")
	fmt.Printf("Input SQL: %s\n", sql)

	// 1. 快速静态检查（避免明显错误）
	if err := t.quickCheck(sql); err != nil {
		result := fmt.Sprintf("❌ SQL validation failed (static check):\n%v\n\nPlease fix the error and try again.", err)
		fmt.Printf("Output: %s\n", result)
		return result, nil
	}

	// 2. 使用数据库执行验证，而不仅仅是 dry-run
	data, err := t.adapter.ExecuteQuery(ctx, sql)
	if err != nil {
		result := fmt.Sprintf("❌ SQL validation failed (database check):\n%v\n\nPlease fix the error and try again.", err)
		fmt.Printf("Output: %s\n", result)
		return result, nil
	}

	// 3. 检查结果行数
	var warnings []string
	if len(data.Rows) == 0 {
		warnings = append(warnings, "⚠️  Warning: Query returned 0 rows. Please double-check:\n  - Are the JOIN conditions correct?\n  - Are the WHERE conditions too restrictive?\n  - Does the data actually exist in the database?")
	}

	// 4. 检查重复行
	rows := convertQueryResultFormat(data.Rows)
	if duplicateWarning := t.checkDuplicateRows(rows); duplicateWarning != "" {
		warnings = append(warnings, duplicateWarning)
	}

	// 5. 构建最终结果
	result := "✓ SQL is valid! You can now provide the final answer."
	if len(warnings) > 0 {
		result += "\n" + strings.Join(warnings, "\n")
	}

	fmt.Printf("Output: %s\n", result)
	return result, nil
}

// quickCheck 快速静态检查
func (t *VerifySQLTool) quickCheck(sql string) error {
	// 1. 检查非法别名（最常见的错误）
	if err := t.checkIllegalAliases(sql); err != nil {
		return err
	}

	// 2. 检查括号匹配
	if err := t.checkParentheses(sql); err != nil {
		return err
	}

	return nil
}

// checkIllegalAliases 检查非法别名
func (t *VerifySQLTool) checkIllegalAliases(sql string) error {
	// 匹配 AS 后面跟着函数调用形式的别名
	// 例如: AS count(*), AS sum(*), AS max(*) 等
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

// checkParentheses 检查括号匹配
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

// NewVerifySQLTool 创建验证工具
func NewVerifySQLTool(adapter adapter.DBAdapter, dbType string) *VerifySQLTool {
	return &VerifySQLTool{
		adapter: adapter,
		dbType:  dbType,
	}
}

// checkDuplicateRows 检查结果中是否有重复行
func (t *VerifySQLTool) checkDuplicateRows(rows [][]string) string {
	if len(rows) <= 2 { // 没有数据行或只有一行数据
		return ""
	}

	seen := make(map[string]bool)
	dataRows := rows[1:] // 排除标题行

	for _, row := range dataRows {
		// 为行创建一个唯一的键
		rowKey := strings.Join(row, "||<SEP>||")
		if seen[rowKey] {
			// 发现重复
			return fmt.Sprintf("Warning: The query returned duplicate rows (e.g., %v). Review the question to determine if duplicates should be removed using DISTINCT.", row)
		}
		seen[rowKey] = true
	}

	return ""
}

// convertQueryResultFormat 将查询结果从 map 转换为二维字符串数组
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
