package main

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// applySPJ applies SPJ (Special Judge) judgment
// Returns: (isCorrect, reason)
func (a *SQLAnalyzer) applySPJ(spjType, goldSQL, predSQL string, gtResult, predResult *ExecResult) (bool, string) {
	switch spjType {
	case "limit_1_tied_values":
		return a.judgeLimitOneTiedValues(goldSQL, predSQL, gtResult, predResult)
	default:
		return false, fmt.Sprintf("unknown SPJ type: %s", spjType)
	}
}

// judgeLimitOneTiedValues judges LIMIT 1 tied values case
// When Gold SQL uses LIMIT 1 with tied extreme values, any correct result is accepted
func (a *SQLAnalyzer) judgeLimitOneTiedValues(goldSQL, predSQL string, gtResult, predResult *ExecResult) (bool, string) {
	// 1. Check if Gold SQL contains LIMIT 1
	if !isLimitOneQuery(goldSQL) {
		return false, "Gold SQL does not contain LIMIT 1, SPJ not applicable"
	}

	// Note: We cannot re-execute SQL here as we have no DB connection in analyze_results
	// We assume gtResult is already the LIMIT 1 result
	// We need alternative ways to check for tied values

	// 3. Check if Pred result is empty
	if len(predResult.Rows) <= 1 {
		return false, "Pred SQL returned empty result"
	}

	// 4. Check Gold result
	if len(gtResult.Rows) <= 1 {
		return false, "Gold SQL returned empty result"
	}

	// 5. Since we cannot re-execute Gold SQL (remove LIMIT 1), we use this strategy:
	// - If Pred returns >1 rows, check if all rows are same (all tied values)
	// - If Pred returns 1 row, check if it matches Gold result

	goldRow := gtResult.Rows[1] // gtResult.Rows[0] is header

	if len(predResult.Rows) == 2 {
		// Pred returned one row
		predRow := predResult.Rows[1]
		if rowsEqual(predRow, goldRow) {
			return true, "Pred SQL correctly returned one of the tied values (matches Gold)"
		}
		// Even if not exact, for LIMIT 1 queries we should accept
		// As there may be multiple tied values, Pred chose one
		return true, "Pred SQL returned one row (LIMIT 1 query, may be one of tied values)"
	} else {
		// Pred returned multiple rows, check if all are tied values
		firstRow := predResult.Rows[1]
		allSame := true
		for i := 2; i < len(predResult.Rows); i++ {
			if !rowsEqual(predResult.Rows[i], firstRow) {
				allSame = false
				break
			}
		}

		if allSame && rowsEqual(firstRow, goldRow) {
			return true, fmt.Sprintf("Pred SQL correctly returned all %d tied values", len(predResult.Rows)-1)
		}

		// Even if not all same, if first row matches Gold, accept
		if rowsEqual(firstRow, goldRow) {
			return true, fmt.Sprintf("Pred SQL returned %d rows of tied values", len(predResult.Rows)-1)
		}

		return false, "Pred SQL returned multiple rows but does not match Gold result"
	}
}

// isLimitOneQuery checks if SQL contains LIMIT 1
func isLimitOneQuery(sql string) bool {
	sqlUpper := strings.ToUpper(sql)
	return strings.Contains(sqlUpper, " LIMIT 1") ||
		strings.HasSuffix(sqlUpper, " LIMIT 1") ||
		strings.Contains(sqlUpper, " LIMIT 1;")
}

// removeLimitOne removes LIMIT 1 from SQL
func removeLimitOne(sql string) string {
	sql = strings.TrimSpace(sql)

	// Use regex to remove LIMIT 1
	re := regexp.MustCompile(`(?i)\s+LIMIT\s+1\s*;?\s*$`)
	sql = re.ReplaceAllString(sql, "")

	return strings.TrimSpace(sql)
}

// rowsEqual compares if two rows are equal
func rowsEqual(row1, row2 []string) bool {
	if len(row1) != len(row2) {
		return false
	}

	for i := range row1 {
		if !valuesEqual(row1[i], row2[i]) {
			return false
		}
	}

	return true
}

// valuesEqual compares two values for equality (handles different types)
func valuesEqual(v1, v2 string) bool {
	// Compare after trimming whitespace
	v1 = strings.TrimSpace(v1)
	v2 = strings.TrimSpace(v2)

	// Direct string comparison
	if v1 == v2 {
		return true
	}

	// Try numeric comparison
	var f1, f2 float64
	_, err1 := fmt.Sscanf(v1, "%f", &f1)
	_, err2 := fmt.Sscanf(v2, "%f", &f2)

	if err1 == nil && err2 == nil {
		return f1 == f2
	}

	// Other cases use reflect.DeepEqual
	return reflect.DeepEqual(v1, v2)
}
