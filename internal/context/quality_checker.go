package context

import (
	"context"
	"fmt"
	"strings"

	"reactsql/internal/adapter"
)

// QualityChecker performs deterministic data quality checks on a table.
// All checks are pure SQL — no LLM involved.
type QualityChecker struct {
	adapter   adapter.DBAdapter
	sharedCtx *SharedContext
	tableName string
	quiet     bool
}

// NewQualityChecker creates a new quality checker for a table
func NewQualityChecker(dbAdapter adapter.DBAdapter, sharedCtx *SharedContext, tableName string) *QualityChecker {
	return &QualityChecker{
		adapter:   dbAdapter,
		sharedCtx: sharedCtx,
		tableName: tableName,
		quiet:     sharedCtx.Quiet,
	}
}

// RunAll executes all quality checks and value stats collection for the table.
// Returns the issues found and updates the SharedContext in place.
func (qc *QualityChecker) RunAll(ctx context.Context) error {
	table, exists := qc.sharedCtx.Tables[qc.tableName]
	if !exists {
		return fmt.Errorf("table %s not found in SharedContext", qc.tableName)
	}

	if table.RowCount == 0 {
		return nil // skip empty tables
	}

	var allIssues []QualityIssue

	// 1. Check quality issues for each column
	for i, col := range table.Columns {
		colType := strings.ToUpper(col.Type)

		// 1a. Whitespace check (TEXT columns only)
		if isTextType(colType) {
			if issue := qc.checkWhitespace(ctx, col.Name); issue != nil {
				allIssues = append(allIssues, *issue)
			}
		}

		// 1b. Type mismatch: TEXT storing purely numeric values
		if isTextType(colType) {
			if issue := qc.checkTypeMismatch(ctx, col.Name, table.RowCount); issue != nil {
				allIssues = append(allIssues, *issue)
			}
		}

		// 1c. Collect value stats for every column
		stats := qc.collectValueStats(ctx, col.Name, colType, table.RowCount)
		if stats != nil {
			table.Columns[i].ValueStats = stats

			// Derive quality issue from stats
			if stats.NullPercent > 50 {
				allIssues = append(allIssues, QualityIssue{
					Table:       qc.tableName,
					Column:      col.Name,
					Type:        "null_heavy",
					Severity:    "warning",
					Description: fmt.Sprintf("%.0f%% NULL values (%d/%d)", stats.NullPercent, stats.NullCount, table.RowCount),
					SQLFix:      fmt.Sprintf("WHERE %s IS NOT NULL", quoteIdent(col.Name)),
					AffectedOps: []string{"WHERE", "JOIN", "GROUP BY"},
				})
			}

			if isTextType(colType) && stats.EmptyCount > 0 {
				allIssues = append(allIssues, QualityIssue{
					Table:       qc.tableName,
					Column:      col.Name,
					Type:        "empty_string",
					Severity:    "warning",
					Description: fmt.Sprintf("Contains %d empty string values in addition to NULLs", stats.EmptyCount),
					SQLFix:      fmt.Sprintf("WHERE %s IS NOT NULL AND %s != ''", quoteIdent(col.Name), quoteIdent(col.Name)),
					AffectedOps: []string{"WHERE", "GROUP BY"},
				})
			}
		}
	}

	// 2. Check orphan records for each foreign key
	for _, fk := range table.ForeignKeys {
		if issue := qc.checkOrphanRecords(ctx, fk); issue != nil {
			allIssues = append(allIssues, *issue)
		}
	}

	// Save to SharedContext
	table.QualityIssues = allIssues

	if !qc.quiet {
		fmt.Printf("[QualityChecker] %s: found %d issues, checked %d columns\n",
			qc.tableName, len(allIssues), len(table.Columns))
	}

	return nil
}

// checkWhitespace checks if a TEXT column contains leading/trailing whitespace
func (qc *QualityChecker) checkWhitespace(ctx context.Context, colName string) *QualityIssue {
	sql := fmt.Sprintf(
		`SELECT %s FROM %s WHERE %s IS NOT NULL AND %s != TRIM(%s) LIMIT 5`,
		quoteIdent(colName), quoteIdent(qc.tableName),
		quoteIdent(colName), quoteIdent(colName), quoteIdent(colName),
	)

	result, err := qc.adapter.ExecuteQuery(ctx, sql)
	if err != nil || result.RowCount == 0 {
		return nil
	}

	// Extract example values
	examples := make([]string, 0, min(3, result.RowCount))
	for _, row := range result.Rows {
		for _, val := range row {
			if s, ok := val.(string); ok {
				examples = append(examples, fmt.Sprintf("'%s'", s))
				if len(examples) >= 3 {
					break
				}
			}
		}
	}

	return &QualityIssue{
		Table:       qc.tableName,
		Column:      colName,
		Type:        "whitespace",
		Severity:    "critical",
		Description: fmt.Sprintf("Contains leading/trailing whitespace (%d+ rows)", result.RowCount),
		SQLFix:      fmt.Sprintf("TRIM(%s)", quoteIdent(colName)),
		AffectedOps: []string{"JOIN", "WHERE", "GROUP BY"},
		Examples:    examples,
	}
}

// checkTypeMismatch checks if a TEXT column stores purely numeric values
func (qc *QualityChecker) checkTypeMismatch(ctx context.Context, colName string, totalRows int64) *QualityIssue {
	// Count non-null, non-empty values
	countSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM %s WHERE %s IS NOT NULL AND %s != ''`,
		quoteIdent(qc.tableName), quoteIdent(colName), quoteIdent(colName),
	)
	countResult, err := qc.adapter.ExecuteQuery(ctx, countSQL)
	if err != nil {
		return nil
	}
	nonEmptyCount := extractCount(countResult)
	if nonEmptyCount < 5 {
		return nil // too few values to judge
	}

	// Count values that look numeric (SQLite CAST trick: CAST as REAL succeeds for numbers)
	// Use a robust check: value = CAST(CAST(value AS REAL) AS TEXT) or similar
	numericSQL := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM %s WHERE %s IS NOT NULL AND %s != '' AND TYPEOF(CAST(%s AS REAL)) = 'real' AND CAST(%s AS REAL) IS NOT NULL AND CAST(CAST(%s AS REAL) AS TEXT) != '0.0' OR (%s = '0' OR %s = '0.0')`,
		quoteIdent(qc.tableName),
		quoteIdent(colName), quoteIdent(colName),
		quoteIdent(colName), quoteIdent(colName), quoteIdent(colName),
		quoteIdent(colName), quoteIdent(colName),
	)

	// Simpler approach: try GLOB pattern for digits (SQLite-specific but we're on SQLite)
	numericSQL = fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM %s WHERE %s IS NOT NULL AND %s != '' AND %s GLOB '[0-9]*' AND %s NOT GLOB '*[a-zA-Z]*'`,
		quoteIdent(qc.tableName),
		quoteIdent(colName), quoteIdent(colName),
		quoteIdent(colName), quoteIdent(colName),
	)

	numResult, err := qc.adapter.ExecuteQuery(ctx, numericSQL)
	if err != nil {
		return nil
	}
	numericCount := extractCount(numResult)

	ratio := float64(numericCount) / float64(nonEmptyCount)
	if ratio < 0.8 {
		return nil // not predominantly numeric
	}

	return &QualityIssue{
		Table:       qc.tableName,
		Column:      colName,
		Type:        "type_mismatch",
		Severity:    "critical",
		Description: fmt.Sprintf("TEXT field storing numeric values (%.0f%% numeric, %d/%d non-empty)", ratio*100, numericCount, nonEmptyCount),
		SQLFix:      fmt.Sprintf("CAST(%s AS INTEGER)", quoteIdent(colName)),
		AffectedOps: []string{"WHERE", "ORDER BY", "GROUP BY", "HAVING"},
	}
}

// checkOrphanRecords checks for orphan records in a foreign key relationship
func (qc *QualityChecker) checkOrphanRecords(ctx context.Context, fk ForeignKeyMetadata) *QualityIssue {
	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM %s child LEFT JOIN %s parent ON child.%s = parent.%s WHERE parent.%s IS NULL AND child.%s IS NOT NULL`,
		quoteIdent(qc.tableName), quoteIdent(fk.ReferencedTable),
		quoteIdent(fk.ColumnName), quoteIdent(fk.ReferencedColumn),
		quoteIdent(fk.ReferencedColumn), quoteIdent(fk.ColumnName),
	)

	result, err := qc.adapter.ExecuteQuery(ctx, sql)
	if err != nil {
		return nil
	}

	orphanCount := extractCount(result)
	if orphanCount == 0 {
		return nil
	}

	return &QualityIssue{
		Table:       qc.tableName,
		Column:      fk.ColumnName,
		Type:        "orphan",
		Severity:    "warning",
		Description: fmt.Sprintf("%d orphan records (%s not in %s.%s)", orphanCount, fk.ColumnName, fk.ReferencedTable, fk.ReferencedColumn),
		SQLFix:      fmt.Sprintf("LEFT JOIN %s ON %s.%s = %s.%s", quoteIdent(fk.ReferencedTable), quoteIdent(qc.tableName), quoteIdent(fk.ColumnName), quoteIdent(fk.ReferencedTable), quoteIdent(fk.ReferencedColumn)),
		AffectedOps: []string{"JOIN"},
	}
}

// collectValueStats collects value statistics for a column
func (qc *QualityChecker) collectValueStats(ctx context.Context, colName, colType string, totalRows int64) *ValueStats {
	if totalRows == 0 {
		return nil
	}

	stats := &ValueStats{}

	// 1. Count NULLs and distinct values
	basicSQL := fmt.Sprintf(
		`SELECT COUNT(*) - COUNT(%s) as null_cnt, COUNT(DISTINCT %s) as distinct_cnt FROM %s`,
		quoteIdent(colName), quoteIdent(colName), quoteIdent(qc.tableName),
	)
	basicResult, err := qc.adapter.ExecuteQuery(ctx, basicSQL)
	if err != nil {
		return nil
	}

	if basicResult.RowCount > 0 {
		row := basicResult.Rows[0]
		stats.NullCount = toInt(row["null_cnt"])
		stats.DistinctCount = toInt(row["distinct_cnt"])
		stats.NullPercent = float64(stats.NullCount) / float64(totalRows) * 100
	}

	// 2. Count empty strings for TEXT columns
	if isTextType(strings.ToUpper(colType)) {
		emptySQL := fmt.Sprintf(
			`SELECT COUNT(*) as cnt FROM %s WHERE %s = ''`,
			quoteIdent(qc.tableName), quoteIdent(colName),
		)
		emptyResult, err := qc.adapter.ExecuteQuery(ctx, emptySQL)
		if err == nil {
			stats.EmptyCount = extractCount(emptyResult)
		}
	}

	// 3. If enumeration type (distinct < 30), collect top values
	if stats.DistinctCount > 0 && stats.DistinctCount <= 30 {
		topSQL := fmt.Sprintf(
			`SELECT %s as val, COUNT(*) as cnt FROM %s WHERE %s IS NOT NULL GROUP BY %s ORDER BY cnt DESC LIMIT 15`,
			quoteIdent(colName), quoteIdent(qc.tableName),
			quoteIdent(colName), quoteIdent(colName),
		)
		topResult, err := qc.adapter.ExecuteQuery(ctx, topSQL)
		if err == nil {
			for _, row := range topResult.Rows {
				val := fmt.Sprintf("%v", row["val"])
				cnt := toInt(row["cnt"])
				stats.TopValues = append(stats.TopValues, ValueFrequency{
					Value:   val,
					Count:   cnt,
					Percent: float64(cnt) / float64(totalRows) * 100,
				})
			}
		}
	}

	// 4. If numeric type, collect range
	upperType := strings.ToUpper(colType)
	if strings.Contains(upperType, "INT") || strings.Contains(upperType, "REAL") ||
		strings.Contains(upperType, "FLOAT") || strings.Contains(upperType, "DOUBLE") ||
		strings.Contains(upperType, "NUMERIC") || strings.Contains(upperType, "DECIMAL") {
		rangeSQL := fmt.Sprintf(
			`SELECT MIN(%s) as min_val, MAX(%s) as max_val, AVG(%s) as avg_val FROM %s WHERE %s IS NOT NULL`,
			quoteIdent(colName), quoteIdent(colName), quoteIdent(colName),
			quoteIdent(qc.tableName), quoteIdent(colName),
		)
		rangeResult, err := qc.adapter.ExecuteQuery(ctx, rangeSQL)
		if err == nil && rangeResult.RowCount > 0 {
			row := rangeResult.Rows[0]
			stats.Range = &NumericRange{
				Min: toFloat64(row["min_val"]),
				Max: toFloat64(row["max_val"]),
				Avg: toFloat64(row["avg_val"]),
			}
		}
	}

	return stats
}

// --- helper functions ---

func isTextType(colType string) bool {
	t := strings.ToUpper(colType)
	return strings.Contains(t, "TEXT") || strings.Contains(t, "VARCHAR") ||
		strings.Contains(t, "CHAR") || strings.Contains(t, "CLOB") ||
		strings.Contains(t, "STRING")
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func extractCount(result *adapter.QueryResult) int {
	if result == nil || result.RowCount == 0 || len(result.Rows) == 0 {
		return 0
	}
	for _, val := range result.Rows[0] {
		return toInt(val)
	}
	return 0
}

func toInt(val interface{}) int {
	switch v := val.(type) {
	case int64:
		return int(v)
	case int:
		return v
	case float64:
		return int(v)
	case int32:
		return int(v)
	default:
		return 0
	}
}

func toFloat64(val interface{}) float64 {
	switch v := val.(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	default:
		return 0
	}
}
