# ReAct SQL 推理管线优化计划

> **目标：** 将 BIRD 数据集准确率从 62.45% 提升到 78-85%
> 
> **当前主要问题：**
> - Row Count Error (273例, 17.8%) - 聚合逻辑错误
> - Data Mismatch (241例, 15.7%) - 字段值匹配错误  
> - Projection Error (56例, 3.7%) - 投影字段错误

---

## Phase 1: Rich Context 结构重构

> **目标：** 分离元数据和业务洞察，提升 Rich Context 质量和可用性
> 
> **预期提升：** 5-8% 准确率，20% Prompt 长度减少
> 
> **工期：** 1-2 周

### 1.1 定义新的数据结构

**文件：** `internal/context/shared_context.go`

**当前问题：**
```json
"rich_context": {
  "car_makers_columns": {...},      // 元数据，不应在此
  "car_makers_foreignkeys": {...},  // 元数据，不应在此
  "Country_quality_issue": {...},   // 质量问题，应结构化
  "Country_values": {...}           // 值分布，应结构化
}
```

**新增数据结构：**

```go
// QualityIssue 结构化的质量问题
type QualityIssue struct {
    Column      string   `json:"column"`
    Type        string   `json:"type"`        // whitespace/type_mismatch/orphan/null/empty
    Severity    string   `json:"severity"`    // critical/warning/info
    Description string   `json:"description"`
    SQLFix      string   `json:"sql_fix"`     // 推荐的修复 SQL 片段
    AffectedOps []string `json:"affected_ops"` // ["JOIN", "WHERE", "ORDER BY"]
    Examples    []string `json:"examples,omitempty"` // 示例值
}

// ValueStats 值统计信息
type ValueStats struct {
    DistinctCount int              `json:"distinct_count"`
    NullCount     int              `json:"null_count"`
    NullPercent   float64          `json:"null_percent"`
    TopValues     []ValueFrequency `json:"top_values,omitempty"`  // 枚举类型
    Range         *NumericRange    `json:"range,omitempty"`       // 数值类型
}

type ValueFrequency struct {
    Value string  `json:"value"`
    Count int     `json:"count"`
    Percent float64 `json:"percent"`
}

type NumericRange struct {
    Min float64 `json:"min"`
    Max float64 `json:"max"`
    Avg float64 `json:"avg"`
}

// ColumnMetadata 增强版列元数据
type ColumnMetadata struct {
    Name          string         `json:"name"`
    Type          string         `json:"type"`
    Nullable      bool           `json:"nullable"`
    IsPrimaryKey  bool           `json:"is_primary_key"`
    DefaultValue  string         `json:"default_value,omitempty"`
    
    // 新增字段
    QualityIssue  *QualityIssue  `json:"quality_issue,omitempty"`
    ValueStats    *ValueStats    `json:"value_stats,omitempty"`
    BusinessNote  string         `json:"business_note,omitempty"`
}

// TableMetadata 重构
type TableMetadata struct {
    Name        string
    Columns     []ColumnMetadata
    Indexes     []IndexMetadata
    ForeignKeys []ForeignKeyMetadata
    RowCount    int
    Description string
    Comment     string
    
    // 质量问题独立出来
    QualityIssues []QualityIssue `json:"quality_issues"`
    
    // 纯业务洞察
    RichContext map[string]RichContextValue `json:"rich_context"`
    
    // 表间关系洞察
    JoinPaths []JoinPath `json:"join_paths,omitempty"`
}

// JoinPath 表间连接路径
type JoinPath struct {
    TargetTable  string   `json:"target_table"`
    Path         []string `json:"path"`         // ["table1.col1 → table2.col2", ...]
    QualityNotes []string `json:"quality_notes,omitempty"` // JOIN 时需要注意的质量问题
}
```

**任务清单：**
- [ ] 在 `shared_context.go` 中定义新结构
- [ ] 添加 JSON 序列化/反序列化测试
- [ ] 更新 `exporter.go` 以支持新格式

---

### 1.2 修改 Worker Agent 收集逻辑

**文件：** `internal/agent/worker_agent.go`

**当前问题：**
- Phase 2 的 Prompt 过长（260行），LLM 容易遗漏步骤
- 质量问题以字符串形式保存，不够结构化
- 缺少值统计信息

**改进方案：**

**1.2.1 拆分 Phase 2 为多个子阶段**

```go
func (a *WorkerAgent) Execute(ctx context.Context) error {
    // Phase 1: 基础元数据收集（保持不变）
    a.collectBasicMetadata(ctx)
    
    // Phase 2: 数据质量检查（新增，强制执行）
    a.checkDataQuality(ctx)
    
    // Phase 3: 值统计收集（新增）
    a.collectValueStats(ctx)
    
    // Phase 4: 业务语义探索（简化的 ReAct）
    a.exploreBusiness(ctx)
    
    // Phase 5: 表描述生成（保持不变）
    a.generateTableDescription(ctx)
}
```

**1.2.2 实现强制数据质量检查**

```go
func (a *WorkerAgent) checkDataQuality(ctx context.Context) error {
    // 获取所有 TEXT 列
    textColumns := a.getTextColumns()
    
    var qualityIssues []QualityIssue
    
    for _, col := range textColumns {
        // 1. 检查空格问题（强制）
        if issue := a.checkWhitespace(ctx, col); issue != nil {
            qualityIssues = append(qualityIssues, *issue)
        }
        
        // 2. 检查类型不匹配（强制）
        if issue := a.checkTypeMismatch(ctx, col); issue != nil {
            qualityIssues = append(qualityIssues, *issue)
        }
        
        // 3. 检查空值情况
        if issue := a.checkNullValues(ctx, col); issue != nil {
            qualityIssues = append(qualityIssues, *issue)
        }
    }
    
    // 4. 检查外键孤儿记录
    for _, fk := range a.sharedCtx.Tables[a.tableName].ForeignKeys {
        if issue := a.checkOrphanRecords(ctx, fk); issue != nil {
            qualityIssues = append(qualityIssues, *issue)
        }
    }
    
    // 保存到 SharedContext
    a.sharedCtx.SetTableQualityIssues(a.tableName, qualityIssues)
    
    return nil
}

func (a *WorkerAgent) checkWhitespace(ctx context.Context, colName string) *QualityIssue {
    sql := fmt.Sprintf("SELECT %s FROM %s WHERE %s != TRIM(%s) LIMIT 3", 
        colName, a.tableName, colName, colName)
    
    result, err := a.adapter.ExecuteQuery(ctx, sql)
    if err != nil || result.RowCount == 0 {
        return nil
    }
    
    // 提取示例值
    examples := make([]string, 0, min(3, result.RowCount))
    for _, row := range result.Rows {
        if val, ok := row[colName].(string); ok {
            examples = append(examples, fmt.Sprintf("'%s'", val))
        }
    }
    
    return &QualityIssue{
        Column:      colName,
        Type:        "whitespace",
        Severity:    "critical",
        Description: "Contains leading/trailing whitespace",
        SQLFix:      fmt.Sprintf("TRIM(%s)", colName),
        AffectedOps: []string{"JOIN", "WHERE", "GROUP BY"},
        Examples:    examples,
    }
}

func (a *WorkerAgent) checkTypeMismatch(ctx context.Context, colName string) *QualityIssue {
    // 检查是否存储纯数字
    sql := fmt.Sprintf("SELECT %s FROM %s WHERE %s GLOB '*[0-9]*' AND %s NOT GLOB '*[a-zA-Z]*' LIMIT 10", 
        colName, a.tableName, colName, colName)
    
    result, err := a.adapter.ExecuteQuery(ctx, sql)
    if err != nil || result.RowCount < 5 {
        return nil
    }
    
    // 检查是否大部分都是数字
    totalSQL := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s IS NOT NULL AND %s != ''", 
        a.tableName, colName, colName)
    totalResult, _ := a.adapter.ExecuteQuery(ctx, totalSQL)
    total := totalResult.Rows[0][0].(int64)
    
    if float64(result.RowCount) / float64(total) > 0.8 {
        return &QualityIssue{
            Column:      colName,
            Type:        "type_mismatch",
            Severity:    "critical",
            Description: "TEXT field storing numeric values",
            SQLFix:      fmt.Sprintf("CAST(%s AS INTEGER)", colName),
            AffectedOps: []string{"WHERE", "ORDER BY", "GROUP BY", "HAVING"},
        }
    }
    
    return nil
}
```

**1.2.3 实现值统计收集**

```go
func (a *WorkerAgent) collectValueStats(ctx context.Context) error {
    for i, col := range a.sharedCtx.Tables[a.tableName].Columns {
        stats := &ValueStats{}
        
        // 1. 统计 NULL 值
        nullSQL := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s IS NULL", 
            a.tableName, col.Name)
        nullResult, _ := a.adapter.ExecuteQuery(ctx, nullSQL)
        stats.NullCount = int(nullResult.Rows[0][0].(int64))
        stats.NullPercent = float64(stats.NullCount) / float64(a.sharedCtx.Tables[a.tableName].RowCount) * 100
        
        // 2. 统计唯一值数量
        distinctSQL := fmt.Sprintf("SELECT COUNT(DISTINCT %s) FROM %s", 
            col.Name, a.tableName)
        distinctResult, _ := a.adapter.ExecuteQuery(ctx, distinctSQL)
        stats.DistinctCount = int(distinctResult.Rows[0][0].(int64))
        
        // 3. 如果是枚举类型（< 20 个唯一值），收集 Top Values
        if stats.DistinctCount > 0 && stats.DistinctCount < 20 {
            topSQL := fmt.Sprintf(`
                SELECT %s, COUNT(*) as cnt 
                FROM %s 
                WHERE %s IS NOT NULL 
                GROUP BY %s 
                ORDER BY cnt DESC 
                LIMIT 10`, col.Name, a.tableName, col.Name, col.Name)
            
            topResult, _ := a.adapter.ExecuteQuery(ctx, topSQL)
            for _, row := range topResult.Rows {
                stats.TopValues = append(stats.TopValues, ValueFrequency{
                    Value:   fmt.Sprintf("%v", row[col.Name]),
                    Count:   int(row["cnt"].(int64)),
                    Percent: float64(row["cnt"].(int64)) / float64(a.sharedCtx.Tables[a.tableName].RowCount) * 100,
                })
            }
        }
        
        // 4. 如果是数值类型，收集范围
        if col.Type == "INTEGER" || col.Type == "REAL" {
            rangeSQL := fmt.Sprintf("SELECT MIN(%s), MAX(%s), AVG(%s) FROM %s WHERE %s IS NOT NULL", 
                col.Name, col.Name, col.Name, a.tableName, col.Name)
            rangeResult, _ := a.adapter.ExecuteQuery(ctx, rangeSQL)
            if rangeResult.RowCount > 0 {
                stats.Range = &NumericRange{
                    Min: toFloat64(rangeResult.Rows[0][0]),
                    Max: toFloat64(rangeResult.Rows[0][1]),
                    Avg: toFloat64(rangeResult.Rows[0][2]),
                }
            }
        }
        
        // 保存到列元数据
        a.sharedCtx.Tables[a.tableName].Columns[i].ValueStats = stats
    }
    
    return nil
}
```

**任务清单：**
- [ ] 实现 `checkDataQuality()` 方法
- [ ] 实现 `checkWhitespace()`, `checkTypeMismatch()`, `checkOrphanRecords()` 方法
- [ ] 实现 `collectValueStats()` 方法
- [ ] 简化 `exploreRichContext()` Prompt（移除质量检查部分）
- [ ] 更新 `SetRichContextTool` 以支持新结构

---

### 1.3 修改 SharedContext 存储结构

**文件：** `internal/context/shared_context.go`

**新增方法：**

```go
// SetTableQualityIssues 设置表的质量问题列表
func (c *SharedContext) SetTableQualityIssues(tableName string, issues []QualityIssue) error {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    table, exists := c.Tables[tableName]
    if !exists {
        return fmt.Errorf("table not found: %s", tableName)
    }
    
    table.QualityIssues = issues
    return nil
}

// SetColumnValueStats 设置列的值统计
func (c *SharedContext) SetColumnValueStats(tableName, columnName string, stats *ValueStats) error {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    table, exists := c.Tables[tableName]
    if !exists {
        return fmt.Errorf("table not found: %s", tableName)
    }
    
    for i, col := range table.Columns {
        if col.Name == columnName {
            table.Columns[i].ValueStats = stats
            return nil
        }
    }
    
    return fmt.Errorf("column not found: %s.%s", tableName, columnName)
}

// GetQualityIssuesByColumn 获取指定列的质量问题
func (c *SharedContext) GetQualityIssuesByColumn(tableName, columnName string) []QualityIssue {
    c.mu.RLock()
    defer c.mu.RUnlock()
    
    table, exists := c.Tables[tableName]
    if !exists {
        return nil
    }
    
    var issues []QualityIssue
    for _, issue := range table.QualityIssues {
        if issue.Column == columnName {
            issues = append(issues, issue)
        }
    }
    
    return issues
}
```

**任务清单：**
- [ ] 添加新方法到 `SharedContext`
- [ ] 更新 `SaveToFile()` 以保存新字段
- [ ] 更新 `LoadFromFile()` 以加载新字段
- [ ] 添加单元测试

---

### 1.4 实现智能 Rich Context 过滤注入

**文件：** `internal/inference/context_filter.go` (新建)

**目标：** 根据查询内容，只注入相关的 Rich Context，避免 Prompt 过长

```go
package inference

import (
    "strings"
    "regexp"
    contextpkg "reactsql/internal/context"
)

// ContextFilter 智能过滤 Rich Context
type ContextFilter struct {
    query    string
    keywords []string
}

// NewContextFilter 创建过滤器
func NewContextFilter(query string) *ContextFilter {
    return &ContextFilter{
        query:    strings.ToLower(query),
        keywords: extractKeywords(query),
    }
}

// FilterRelevantContext 过滤相关的 Rich Context
func (f *ContextFilter) FilterRelevantContext(tables []string, allContext map[string]*contextpkg.TableMetadata) *FilteredContext {
    result := &FilteredContext{
        QualityIssues: make(map[string][]contextpkg.QualityIssue),
        ValueStats:    make(map[string]map[string]*contextpkg.ValueStats),
        BusinessNotes: make(map[string]map[string]string),
    }
    
    for _, tableName := range tables {
        table := allContext[tableName]
        
        // 1. 过滤质量问题（只保留相关列的）
        for _, issue := range table.QualityIssues {
            if f.isColumnRelevant(issue.Column) {
                result.QualityIssues[tableName] = append(result.QualityIssues[tableName], issue)
            }
        }
        
        // 2. 过滤值统计（只保留枚举类型的）
        result.ValueStats[tableName] = make(map[string]*contextpkg.ValueStats)
        for _, col := range table.Columns {
            if col.ValueStats != nil && len(col.ValueStats.TopValues) > 0 {
                if f.isColumnRelevant(col.Name) {
                    result.ValueStats[tableName][col.Name] = col.ValueStats
                }
            }
        }
        
        // 3. 过滤业务注释
        result.BusinessNotes[tableName] = make(map[string]string)
        for key, value := range table.RichContext {
            // 只保留业务规则和含义说明
            if strings.Contains(key, "business_rule") || 
               strings.Contains(key, "meaning") ||
               strings.Contains(key, "domain") {
                result.BusinessNotes[tableName][key] = value.Content
            }
        }
    }
    
    return result
}

// isColumnRelevant 判断列是否与查询相关
func (f *ContextFilter) isColumnRelevant(columnName string) bool {
    colLower := strings.ToLower(columnName)
    
    // 1. 直接匹配列名
    if strings.Contains(f.query, colLower) {
        return true
    }
    
    // 2. 匹配关键词
    for _, keyword := range f.keywords {
        if strings.Contains(colLower, keyword) || strings.Contains(keyword, colLower) {
            return true
        }
    }
    
    return false
}

// extractKeywords 提取查询中的关键词
func extractKeywords(query string) []string {
    // 移除停用词
    stopWords := map[string]bool{
        "the": true, "a": true, "an": true, "and": true, "or": true,
        "in": true, "on": true, "at": true, "to": true, "for": true,
        "of": true, "with": true, "by": true, "from": true, "is": true,
        "are": true, "was": true, "were": true, "what": true, "which": true,
        "how": true, "many": true, "much": true, "list": true, "show": true,
    }
    
    // 提取单词
    re := regexp.MustCompile(`\b\w+\b`)
    words := re.FindAllString(strings.ToLower(query), -1)
    
    var keywords []string
    for _, word := range words {
        if !stopWords[word] && len(word) > 2 {
            keywords = append(keywords, word)
        }
    }
    
    return keywords
}

type FilteredContext struct {
    QualityIssues map[string][]contextpkg.QualityIssue              // table -> issues
    ValueStats    map[string]map[string]*contextpkg.ValueStats      // table -> column -> stats
    BusinessNotes map[string]map[string]string                      // table -> key -> note
}
```

**任务清单：**
- [ ] 创建 `context_filter.go`
- [ ] 实现 `FilterRelevantContext()` 方法
- [ ] 在 `Pipeline.Execute()` 中集成过滤器
- [ ] 添加单元测试

---

## Phase 2: 推理流程增强

> **目标：** 增加质量问题感知、值验证、结果验证等步骤
> 
> **预期提升：** 10-15% 准确率
> 
> **工期：** 2-3 周

### 2.1 在 Pipeline 中增加质量问题分析阶段

**文件：** `internal/inference/pipeline.go`

**当前流程：**
```
1. Schema Linking → 2. SQL Generation
```

**新流程：**
```
1. Schema Linking → 1.5. Quality Issue Analysis → 2. SQL Generation
```

**实现：**

```go
func (p *Pipeline) Execute(ctx context.Context, query string) (*Result, error) {
    // 1. Schema Linking
    selectedTables, linkingSteps, err := p.linker.Link(ctx, query, allTableInfo)
    
    // 1.5. Quality Issue Analysis (新增)
    qualityIssues := p.analyzeQualityIssues(selectedTables)
    
    // 2. SQL Generation (注入质量问题信息)
    sql, genSteps, err := p.generateSQL(ctx, query, selectedTables, qualityIssues)
    
    return result, nil
}

func (p *Pipeline) analyzeQualityIssues(tables []string) []contextpkg.QualityIssue {
    var allIssues []contextpkg.QualityIssue
    
    for _, tableName := range tables {
        table := p.context.Tables[tableName]
        
        // 收集该表的所有 critical 和 warning 级别的质量问题
        for _, issue := range table.QualityIssues {
            if issue.Severity == "critical" || issue.Severity == "warning" {
                allIssues = append(allIssues, issue)
            }
        }
    }
    
    // 按严重性和影响范围排序
    sort.Slice(allIssues, func(i, j int) bool {
        if allIssues[i].Severity != allIssues[j].Severity {
            return allIssues[i].Severity == "critical"
        }
        return len(allIssues[i].AffectedOps) > len(allIssues[j].AffectedOps)
    })
    
    return allIssues
}
```

**任务清单：**
- [ ] 在 `Pipeline.Execute()` 中添加质量问题分析步骤
- [ ] 修改 `generateSQL()` 接受质量问题参数
- [ ] 在 SQL Generation Prompt 中注入质量问题

---

### 2.2 实现 verify_value 工具

**文件：** `internal/inference/verify_value_tool.go` (新建)

**目标：** 在生成 WHERE 条件前，验证字段值是否存在

```go
package inference

import (
    "context"
    "fmt"
    "strings"
    "reactsql/internal/adapter"
)

// VerifyValueTool 验证字段值是否存在
type VerifyValueTool struct {
    adapter adapter.DBAdapter
    logger  *InferenceLogger
}

func (t *VerifyValueTool) Name() string {
    return "verify_value"
}

func (t *VerifyValueTool) Description() string {
    return `Verify if a value exists in a column BEFORE writing WHERE conditions.

Input format: table.column|value

Examples:
- frpm."Educational Option Type"|Continuation School
- schools.Charter|1
- cars_data.Cylinders|4

Returns: 
- "EXISTS (count: N)" if value found
- "NOT FOUND. Suggestions: [value1, value2, ...]" with similar values
`
}

func (t *VerifyValueTool) Call(ctx context.Context, input string) (string, error) {
    // 解析输入
    parts := strings.SplitN(input, "|", 2)
    if len(parts) != 2 {
        return "", fmt.Errorf("invalid format, expected: table.column|value")
    }
    
    tableCol := parts[0]
    value := strings.TrimSpace(parts[1])
    
    // 解析 table.column
    tableParts := strings.SplitN(tableCol, ".", 2)
    if len(tableParts) != 2 {
        return "", fmt.Errorf("invalid format, expected: table.column")
    }
    
    tableName := strings.Trim(tableParts[0], `"`)
    columnName := strings.Trim(tableParts[1], `"`)
    
    // 1. 检查值是否存在
    checkSQL := fmt.Sprintf(`SELECT COUNT(*) FROM "%s" WHERE "%s" = '%s'`, 
        tableName, columnName, strings.ReplaceAll(value, "'", "''"))
    
    if t.logger != nil {
        t.logger.FileOnly("  [verify_value] SQL: %s\n", checkSQL)
    }
    
    result, err := t.adapter.ExecuteQuery(ctx, checkSQL)
    if err != nil {
        return "", fmt.Errorf("query failed: %w", err)
    }
    
    count := result.Rows[0][0].(int64)
    
    if count > 0 {
        return fmt.Sprintf("✓ EXISTS (count: %d)", count), nil
    }
    
    // 2. 值不存在，提供建议
    suggestSQL := fmt.Sprintf(`SELECT DISTINCT "%s" FROM "%s" WHERE "%s" IS NOT NULL LIMIT 10`, 
        columnName, tableName, columnName)
    
    suggestResult, err := t.adapter.ExecuteQuery(ctx, suggestSQL)
    if err != nil {
        return "✗ NOT FOUND (unable to get suggestions)", nil
    }
    
    var suggestions []string
    for _, row := range suggestResult.Rows {
        if val := row[columnName]; val != nil {
            suggestions = append(suggestions, fmt.Sprintf("%v", val))
        }
    }
    
    return fmt.Sprintf("✗ NOT FOUND. Available values: %s", strings.Join(suggestions, ", ")), nil
}
```

**集成到 ReAct：**

```go
// 在 react.go 的 runReActLoop() 中添加工具
tools := []tools.Tool{
    sqlTool,
    verifySQLTool,
    &VerifyValueTool{adapter: p.adapter, logger: p.Logger},  // 新增
}
```

**任务清单：**
- [ ] 创建 `verify_value_tool.go`
- [ ] 实现 `VerifyValueTool`
- [ ] 集成到 ReAct 工具列表
- [ ] 在 Prompt 中说明何时使用该工具

---

### 2.3 在 SQL Generation Prompt 中增加 SQL 模式约束

**文件：** `internal/inference/react.go`

**在 `buildReActPrompt()` 中添加：**

```go
func (p *Pipeline) buildReActPrompt(query string, tables []string, qualityIssues []QualityIssue) string {
    var prompt strings.Builder
    
    // ... 现有内容 ...
    
    // 新增：SQL 模式约束
    prompt.WriteString(`
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
📋 SQL PATTERN RULES (CRITICAL - Follow these strictly)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

1. "Highest/Lowest N items" or "Top N":
   ✅ CORRECT: SELECT ... ORDER BY column DESC/ASC LIMIT N
   ❌ WRONG:   SELECT MAX(column) ... (returns only 1 row, not N rows)
   
   Example:
   Q: "List top 3 highest prices"
   ✅ SELECT * FROM products ORDER BY price DESC LIMIT 3
   ❌ SELECT MAX(price) FROM products  -- Only returns 1 value!

2. "Group by X and aggregate":
   ✅ MUST include: GROUP BY X
   ❌ Common mistake: Aggregate without GROUP BY
   
   Example:
   Q: "Count products by category"
   ✅ SELECT category, COUNT(*) FROM products GROUP BY category
   ❌ SELECT category, COUNT(*) FROM products  -- Missing GROUP BY!

3. "Rate/Percentage calculation":
   ✅ USE: CAST(numerator AS REAL) / CAST(denominator AS REAL)
   ❌ AVOID: Integer division (e.g., 1/2 = 0 in SQLite)
   
   Example:
   ✅ SELECT CAST(passed AS REAL) / CAST(total AS REAL) * 100
   ❌ SELECT passed / total * 100  -- Returns 0!

4. "Text field with quality issues":
   ✅ USE: TRIM(column) in JOIN/WHERE
   ✅ USE: CAST(column AS INTEGER) for numeric operations
   
   See quality issues below for specific columns.

5. "Foreign key with orphan records":
   ✅ USE: LEFT JOIN (preserves all records)
   ❌ AVOID: INNER JOIN (loses orphan records)

6. "Distinct values" or "Unique items":
   ✅ USE: SELECT DISTINCT column ...
   ❌ AVOID: GROUP BY without aggregation

`)
    
    // 新增：质量问题警告
    if len(qualityIssues) > 0 {
        prompt.WriteString(`
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
⚠️  CRITICAL DATA QUALITY ISSUES (MUST ADDRESS)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

`)
        for i, issue := range qualityIssues {
            prompt.WriteString(fmt.Sprintf("%d. Column: %s.%s\n", i+1, issue.Table, issue.Column))
            prompt.WriteString(fmt.Sprintf("   Issue: %s\n", issue.Description))
            prompt.WriteString(fmt.Sprintf("   Fix: %s\n", issue.SQLFix))
            prompt.WriteString(fmt.Sprintf("   Affects: %s\n\n", strings.Join(issue.AffectedOps, ", ")))
        }
    }
    
    return prompt.String()
}
```

**任务清单：**
- [ ] 在 `buildReActPrompt()` 中添加 SQL 模式规则
- [ ] 添加质量问题警告部分
- [ ] 测试 Prompt 长度，确保不超过模型限制

---

### 2.4 实现 verify_result 工具

**文件：** `internal/inference/verify_result_tool.go` (新建)

**目标：** 在 Final Answer 前，验证 SQL 结果的合理性

```go
package inference

import (
    "context"
    "fmt"
    "reactsql/internal/adapter"
)

// VerifyResultTool 验证 SQL 结果合理性
type VerifyResultTool struct {
    adapter adapter.DBAdapter
    logger  *InferenceLogger
}

func (t *VerifyResultTool) Name() string {
    return "verify_result"
}

func (t *VerifyResultTool) Description() string {
    return `Verify if the generated SQL returns reasonable results BEFORE Final Answer.

Use this to check:
1. Row count (is it reasonable given the question?)
2. Sample results (do they make sense?)
3. NULL values (are there unexpected NULLs?)

Input: Your SQL query (without semicolon)

Returns: Verification report with row count and sample results.
`
}

func (t *VerifyResultTool) Call(ctx context.Context, sql string) (string, error) {
    var report strings.Builder
    
    report.WriteString("━━━━ SQL Verification Report ━━━━\n\n")
    
    // 1. 检查行数
    countSQL := fmt.Sprintf("SELECT COUNT(*) FROM (%s)", sql)
    countResult, err := t.adapter.ExecuteQuery(ctx, countSQL)
    if err != nil {
        return "", fmt.Errorf("count query failed: %w", err)
    }
    
    rowCount := countResult.Rows[0][0].(int64)
    report.WriteString(fmt.Sprintf("📊 Row Count: %d\n\n", rowCount))
    
    // 2. 采样结果
    sampleSQL := fmt.Sprintf("%s LIMIT 3", sql)
    sampleResult, err := t.adapter.ExecuteQuery(ctx, sampleSQL)
    if err != nil {
        return "", fmt.Errorf("sample query failed: %w", err)
    }
    
    report.WriteString("📋 Sample Results (first 3 rows):\n")
    if sampleResult.RowCount == 0 {
        report.WriteString("  (No results)\n\n")
    } else {
        for i, row := range sampleResult.Rows {
            report.WriteString(fmt.Sprintf("  Row %d: %v\n", i+1, row))
        }
        report.WriteString("\n")
    }
    
    // 3. 检查 NULL 值
    if sampleResult.RowCount > 0 {
        hasNull := false
        for _, row := range sampleResult.Rows {
            for _, val := range row {
                if val == nil {
                    hasNull = true
                    break
                }
            }
        }
        
        if hasNull {
            report.WriteString("⚠️  Warning: Results contain NULL values\n\n")
        }
    }
    
    // 4. 合理性提示
    report.WriteString("❓ Does this look correct?\n")
    report.WriteString("   - If row count seems wrong, check GROUP BY or JOIN conditions\n")
    report.WriteString("   - If sample values look wrong, check WHERE conditions\n")
    report.WriteString("   - If you see NULLs, check if you need IS NOT NULL filters\n\n")
    report.WriteString("If everything looks good, proceed to Final Answer.\n")
    report.WriteString("If not, revise your SQL and verify again.\n")
    
    return report.String(), nil
}
```

**任务清单：**
- [ ] 创建 `verify_result_tool.go`
- [ ] 实现 `VerifyResultTool`
- [ ] 集成到 ReAct 工具列表
- [ ] 在 Prompt 中强调使用时机

---

## Phase 3: Few-shot 示例和测试验证

> **目标：** 构建 BIRD 特定的示例库，测试优化效果
> 
> **预期提升：** 5-8% 准确率
> 
> **工期：** 1 周

### 3.1 构建 BIRD 特定的 Few-shot 示例库

**文件：** `internal/inference/bird_examples.go` (新建)

```go
package inference

// BirdExample BIRD 数据集的典型示例
type BirdExample struct {
    Question    string
    Database    string
    GoodSQL     string
    BadSQL      string
    Explanation string
    Category    string // whitespace/type_mismatch/aggregation/join
}

var BirdExamples = []BirdExample{
    {
        Question: "What is the average horsepower of cars?",
        Database: "car_1",
        GoodSQL: `SELECT AVG(CAST(Horsepower AS INTEGER)) 
FROM cars_data 
WHERE Horsepower != '' AND Horsepower != 'N/A'`,
        BadSQL: `SELECT AVG(Horsepower) FROM cars_data`,
        Explanation: "Horsepower is TEXT storing numbers. Must CAST and filter invalid values.",
        Category: "type_mismatch",
    },
    {
        Question: "List car makes and their models",
        Database: "car_1",
        GoodSQL: `SELECT cn.Make, ml.Model 
FROM car_names cn 
LEFT JOIN model_list ml ON TRIM(cn.Model) = TRIM(ml.Model)`,
        BadSQL: `SELECT cn.Make, ml.Model 
FROM car_names cn 
JOIN model_list ml ON cn.Model = ml.Model`,
        Explanation: "Model column has whitespace. Use TRIM() and LEFT JOIN for orphan records.",
        Category: "whitespace",
    },
    {
        Question: "What are the top 3 cities with lowest enrollment?",
        Database: "california_schools",
        GoodSQL: `SELECT City 
FROM schools s 
JOIN frpm f ON s.CDSCode = f.CDSCode 
GROUP BY City 
ORDER BY SUM(f."Enrollment (K-12)") ASC 
LIMIT 3`,
        BadSQL: `SELECT City 
FROM schools s 
JOIN frpm f ON s.CDSCode = f.CDSCode 
ORDER BY f."Enrollment (K-12)" ASC 
LIMIT 3`,
        Explanation: "Must GROUP BY City and use SUM() for aggregation, not individual rows.",
        Category: "aggregation",
    },
}

// SelectRelevantExamples 选择与查询相关的示例
func SelectRelevantExamples(query string, maxExamples int) []BirdExample {
    // 基于查询关键词和质量问题类型选择示例
    // 实现略
}
```

**任务清单：**
- [ ] 创建 `bird_examples.go`
- [ ] 收集 20-30 个典型错误案例
- [ ] 实现 `SelectRelevantExamples()` 方法
- [ ] 在 Prompt 中动态注入相关示例

---

### 3.2 在现有数据集上测试优化效果

**测试计划：**

1. **基准测试**
   - 在优化前的代码上运行 BIRD dev set（1534 examples）
   - 记录准确率、错误类型分布

2. **Phase 1 测试**
   - 应用 Rich Context 结构优化
   - 重新生成所有 Rich Context
   - 运行评测，对比改进

3. **Phase 2 测试**
   - 应用推理流程优化
   - 运行评测，对比改进

4. **Phase 3 测试**
   - 添加 Few-shot 示例
   - 运行评测，对比改进

**任务清单：**
- [ ] 建立测试脚本 `scripts/test_optimization.sh`
- [ ] 记录每个阶段的评测结果
- [ ] 生成对比报告

---

### 3.3 分析错误案例，迭代优化

**错误分析流程：**

1. **收集失败案例**
   ```bash
   grep '❌' results/bird/*/inference.log > failed_cases.txt
   ```

2. **分类错误类型**
   - Row Count Error
   - Data Mismatch
   - Projection Error
   - 其他

3. **分析根因**
   - 质量问题未被检测？
   - Prompt 不够清晰？
   - 工具使用不当？

4. **针对性优化**
   - 改进质量检查逻辑
   - 优化 Prompt
   - 添加新的 Few-shot 示例

**任务清单：**
- [ ] 创建错误分析脚本
- [ ] 分析 Top 20 失败案例
- [ ] 针对性改进
- [ ] 重新测试验证

---

## 实施时间表

| 阶段 | 任务 | 工期 | 负责人 |
|------|------|------|--------|
| **Week 1-2** | Phase 1.1-1.3: Rich Context 结构重构 | 2 周 | - |
| **Week 3** | Phase 1.4: 智能过滤注入 | 1 周 | - |
| **Week 4-5** | Phase 2.1-2.2: 质量感知 + verify_value | 2 周 | - |
| **Week 6** | Phase 2.3-2.4: SQL 模式 + verify_result | 1 周 | - |
| **Week 7** | Phase 3: Few-shot + 测试 | 1 周 | - |
| **Week 8** | 错误分析 + 迭代优化 | 1 周 | - |

**总工期：8 周**

---

## 预期成果

### 定量指标

| 指标 | 当前 | 目标 | 提升 |
|------|------|------|------|
| **BIRD 准确率** | 62.45% | 78-85% | +15-22% |
| **Row Count Error** | 17.8% | < 8% | -10% |
| **Data Mismatch** | 15.7% | < 5% | -11% |
| **Projection Error** | 3.7% | < 2% | -2% |
| **Prompt 长度** | ~8000 tokens | ~6000 tokens | -25% |

### 定性改进

1. ✅ Rich Context 更结构化、更易维护
2. ✅ 质量问题检测更全面、更准确
3. ✅ 推理过程更透明、更可控
4. ✅ 错误率显著降低
5. ✅ 代码可读性和可扩展性提升

---

## 风险和应对

| 风险 | 影响 | 概率 | 应对措施 |
|------|------|------|----------|
| Prompt 过长超出模型限制 | 高 | 中 | 实施智能过滤，只注入相关信息 |
| 质量检查耗时过长 | 中 | 中 | 并行执行，增加超时控制 |
| 新工具导致 ReAct 迭代增加 | 中 | 低 | 优化 Prompt，明确工具使用时机 |
| 改动过大引入新 bug | 高 | 中 | 分阶段实施，每阶段充分测试 |

---

## 附录

### A. 相关文件清单

**需要修改的文件：**
- `internal/context/shared_context.go`
- `internal/agent/worker_agent.go`
- `internal/inference/pipeline.go`
- `internal/inference/react.go`
- `internal/inference/schema_linker.go`

**需要新建的文件：**
- `internal/inference/context_filter.go`
- `internal/inference/verify_value_tool.go`
- `internal/inference/verify_result_tool.go`
- `internal/inference/bird_examples.go`

### B. 测试用例

**单元测试：**
- `context_filter_test.go`
- `verify_value_tool_test.go`
- `verify_result_tool_test.go`

**集成测试：**
- `pipeline_optimization_test.go`

### C. 文档更新

- [ ] 更新 `README.md` 说明新功能
- [ ] 更新 `contexts/DATA_QUALITY_REPORT.md`
- [ ] 创建 `docs/OPTIMIZATION_GUIDE.md`

---

---

## E2E 验证总结（2026-02-23）

### 验证范围

对 `car_1` / `world_1` / `flight_2` / `concert_singer` / `student_transcripts_tracking` 共 5 个数据库各 10 个问题，无缓存实时运行 QualityChecker + SchemaLinker + Prompt 注入，验证前置管线。

### 结论：RC 生成 → SchemaLinker 抽取 → Prompt 注入管线 **代码逻辑正确，无需修改**

完整链路：`SharedContext.LoadFromFile()` → `QualityChecker.RunAll()` → `ExportToCompactPrompt()` → `ExtractTableInfo()` → `SchemaLinker.Link()` → `buildPrompt()` — 当 SharedContext 中有 QualityIssues 和 ValueStats 数据时，输出完全符合预期：

- quality issues 在 compact prompt 中正确展示为 `⚠️ QUALITY ISSUES` 区块
- value stats 在列定义后正确内联为 `values=[...]` / `range=[...]` 注解
- `BuildCrossTableQualitySummary()` 正确汇总跨表质量警告
- SchemaLinker 的 `QualitySummary` 字段正确传递 critical 列信息
- Prompt 长度合理（827~3137 tokens，取决于表数量）

### 当前唯一问题：RC JSON 数据为空

现有 20 个 context JSON 是在 QualityChecker 代码写入之前生成的：

| 数据库 | quality_issues | value_stats |
|--------|---------------|-------------|
| car_1 | 0 | 0 |
| world_1 | 0 | 0 |
| flight_2 | 0 | 0 |
| concert_singer | 0 | 0 |

**解决方案：重新跑 `gen_all_dev` 生成 context JSON，让 Worker Agent Phase 1.5 的 QualityChecker 结果持久化。**

### 附带发现

1. **部分表 row_count=0**：`concert_singer.singer_in_concert`/`stadium`、`car_1.model_list` 在 JSON 中记录为 0 行（实际有数据），需排查 Worker Agent Phase 1 的 `collectBasicMetadata` 解析逻辑。
2. **Business Notes 大量 [EXPIRED]**：7 天过期机制导致确定性信息（值分布等）也被标记过期，建议只对 LLM 主观判断类信息保留过期机制。

---

## 下一步：推理管线优化 TODO

### P0（立即执行）

#### 1. 重新生成所有 context JSON
- 跑 `gen_all_dev`，更新 20 个数据库的 JSON，让 QualityIssues + ValueStats 写入
- 验证 row_count=0 的 bug 是否在重新生成后修复
- 预计耗时：~2h（LLM 调用为主）

#### 2. Pipeline.Execute 中加入 QualityChecker 调用
- 位置：`pipeline.go` 的 `loadContext()` 之后、`ExtractTableInfo()` 之前
- QualityChecker 是纯确定性 SQL 检查，不调 LLM，耗时 <100ms
- 作用：即使 JSON 过期，推理时也能拿到最新的质量检查结果
- 代码改动量：~15 行

### P1（本周内）

#### 3. SQL Best Practices 精简 + 模式约束合并
- 现有 react.go 中有 9 条 Best Practices，与计划中 2.3 的 6 条模式规则有重叠
- 合并去重后控制在 10 条以内，避免 Prompt 注意力稀释
- 将 QualityChecker 发现的**具体问题**（如 `cars_data.Horsepower` 是 TEXT 存数字）注入到 Best Practices 下方，用 `-- ⚠️ Specific data issues for this query:` 分隔

#### 4. verify_sql 工具增强
- 把 verify_result 的合理性检查（结果行数、NULL 比例）合并到现有 `VerifySQLTool`
- 不新建工具，避免增加 LLM 选择负担
- 具体增加：空结果时建议放宽条件、结果异常大时警告

#### 5. Business Notes 过期机制优化
- 确定性信息（值分布、类型统计）：不过期
- LLM 主观判断（业务规则推断等）：保留 7 天过期
- 修改 `exporter.go` 中 `isExpired()` 逻辑

### P2（下周）

#### 6. Few-shot 示例注入
- 按错误类型（而非按数据库）组织示例，JSON 文件存储
- 格式紧凑：只保留 Good/Bad SQL 对 + 一行 Explanation，不含完整 Question
- 控制在 2-3 个示例，~400 tokens
- 基于 SchemaLinker 选出的表 + QualityChecker 检出的问题类型，动态选择最相关示例

#### 7. 跑 BIRD dev subset 基准评测
- 随机选 200 题作为快速验证集
- 在 P0/P1 改完后各跑一次，对比改动前后准确率
- 记录：整体准确率、按错误类型分布、Prompt 平均 token 数

---

**文档版本：** v1.1  
**创建日期：** 2026-02-23  
**最后更新：** 2026-02-23
