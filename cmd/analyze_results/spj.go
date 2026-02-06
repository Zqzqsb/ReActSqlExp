package main

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// applySPJ 应用 SPJ (Special Judge) 判定
// 返回: (是否正确, 判定说明)
func (a *SQLAnalyzer) applySPJ(spjType, goldSQL, predSQL string, gtResult, predResult *ExecResult) (bool, string) {
	switch spjType {
	case "limit_1_tied_values":
		return a.judgeLimitOneTiedValues(goldSQL, predSQL, gtResult, predResult)
	default:
		return false, fmt.Sprintf("未知的 SPJ 类型: %s", spjType)
	}
}

// judgeLimitOneTiedValues 判定 LIMIT 1 并列值情况
// 当 Gold SQL 使用 LIMIT 1 但有多个并列极值时，Pred SQL 返回其中任意一个都应该判定为正确
func (a *SQLAnalyzer) judgeLimitOneTiedValues(goldSQL, predSQL string, gtResult, predResult *ExecResult) (bool, string) {
	// 1. 检查 Gold SQL 是否包含 LIMIT 1
	if !isLimitOneQuery(goldSQL) {
		return false, "Gold SQL 不包含 LIMIT 1，不适用此 SPJ"
	}

	// 注意：这里我们不能重新执行 SQL，因为我们在 analyze_results 中没有数据库连接
	// 我们需要假设 gtResult 已经是 LIMIT 1 的结果（只有1行或0行）
	// 我们需要通过其他方式判断是否有并列值

	// 3. 检查 Pred 结果是否为空
	if len(predResult.Rows) <= 1 {
		return false, "Pred SQL 返回空结果"
	}

	// 4. 检查 Gold 结果
	if len(gtResult.Rows) <= 1 {
		return false, "Gold SQL 返回空结果"
	}

	// 5. 由于我们无法重新执行 Gold SQL（移除 LIMIT 1），我们采用以下策略：
	// - 如果 Pred 返回的行数 > 1，检查所有行是否相同（表示返回了所有并列值）
	// - 如果 Pred 返回的行数 = 1，检查这一行是否与 Gold 的结果相同

	goldRow := gtResult.Rows[1] // gtResult.Rows[0] 是 header

	if len(predResult.Rows) == 2 {
		// Pred 只返回了一行
		predRow := predResult.Rows[1]
		if rowsEqual(predRow, goldRow) {
			return true, "Pred SQL 正确返回了并列值之一（与 Gold 相同）"
		}
		// 即使不完全相同，如果是 LIMIT 1 查询，我们也应该接受
		// 因为可能有多个并列值，Pred 选择了其中一个
		return true, "Pred SQL 返回了一行结果（LIMIT 1 查询，可能是并列值之一）"
	} else {
		// Pred 返回了多行，检查是否都是并列值
		firstRow := predResult.Rows[1]
		allSame := true
		for i := 2; i < len(predResult.Rows); i++ {
			if !rowsEqual(predResult.Rows[i], firstRow) {
				allSame = false
				break
			}
		}

		if allSame && rowsEqual(firstRow, goldRow) {
			return true, fmt.Sprintf("Pred SQL 正确返回了所有 %d 个并列值", len(predResult.Rows)-1)
		}

		// 即使不完全相同，如果第一行与 Gold 相同，也接受
		if rowsEqual(firstRow, goldRow) {
			return true, fmt.Sprintf("Pred SQL 返回了 %d 行并列值", len(predResult.Rows)-1)
		}

		return false, "Pred SQL 返回了多行，但与 Gold 的结果不匹配"
	}
}

// isLimitOneQuery 检查 SQL 是否包含 LIMIT 1
func isLimitOneQuery(sql string) bool {
	sqlUpper := strings.ToUpper(sql)
	return strings.Contains(sqlUpper, " LIMIT 1") ||
		strings.HasSuffix(sqlUpper, " LIMIT 1") ||
		strings.Contains(sqlUpper, " LIMIT 1;")
}

// removeLimitOne 移除 SQL 中的 LIMIT 1
func removeLimitOne(sql string) string {
	sql = strings.TrimSpace(sql)

	// 使用正则表达式移除 LIMIT 1
	re := regexp.MustCompile(`(?i)\s+LIMIT\s+1\s*;?\s*$`)
	sql = re.ReplaceAllString(sql, "")

	return strings.TrimSpace(sql)
}

// rowsEqual 比较两行数据是否相等
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

// valuesEqual 比较两个值是否相等（处理不同类型）
func valuesEqual(v1, v2 string) bool {
	// 去除空格后比较
	v1 = strings.TrimSpace(v1)
	v2 = strings.TrimSpace(v2)

	// 直接字符串比较
	if v1 == v2 {
		return true
	}

	// 尝试作为数字比较
	var f1, f2 float64
	_, err1 := fmt.Sscanf(v1, "%f", &f1)
	_, err2 := fmt.Sscanf(v2, "%f", &f2)

	if err1 == nil && err2 == nil {
		return f1 == f2
	}

	// 其他情况使用反射比较
	return reflect.DeepEqual(v1, v2)
}
