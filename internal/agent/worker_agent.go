package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/tools"

	"reactsql/internal/adapter"
	contextpkg "reactsql/internal/context"
)

// WorkerAgent worker agent
type WorkerAgent struct {
	id        string
	taskID    string
	tableName string
	llm       llms.Model
	adapter   adapter.DBAdapter
	sharedCtx *contextpkg.SharedContext
	executor  *agents.Executor
}

// NewWorkerAgent creates worker agent
func NewWorkerAgent(
	id string,
	taskID string,
	tableName string,
	llm llms.Model,
	adapter adapter.DBAdapter,
	sharedCtx *contextpkg.SharedContext,
) (*WorkerAgent, error) {

	agent := &WorkerAgent{
		id:        id,
		taskID:    taskID,
		tableName: tableName,
		llm:       llm,
		adapter:   adapter,
		sharedCtx: sharedCtx,
	}

	// Create tools
	sqlTool := &WorkerSQLTool{
		adapter:   adapter,
		sharedCtx: sharedCtx,
		agentID:   id,
		tableName: tableName,
	}

	richContextTool := &SetRichContextTool{
		sharedCtx: sharedCtx,
		agentID:   id,
		tableName: tableName,
	}

	// Create LangChain executor
	executor, err := agents.Initialize(
		llm,
		[]tools.Tool{sqlTool, richContextTool},
		agents.ZeroShotReactDescription,
		agents.WithMaxIterations(25), // Increase iterations for complex table analysis
	)
	if err != nil {
		return nil, err
	}

	agent.executor = executor
	return agent, nil
}

// Execute runs analysis task (multi-phase)
func (a *WorkerAgent) Execute(ctx context.Context) error {
	if !a.sharedCtx.Quiet {
		fmt.Printf("\n[%s] Starting analysis of table '%s'...\n", a.id, a.tableName)
	}

	// marks task started
	if err := a.sharedCtx.StartTask(a.taskID); err != nil {
		return err
	}

	// ========== Phase 1: Collect basic metadata ==========
	if !a.sharedCtx.Quiet {
		fmt.Printf("\n[%s] Phase 1: Collecting basic metadata...\n", a.id)
	}
	if err := a.collectBasicMetadata(ctx); err != nil {
		a.sharedCtx.FailTask(a.taskID, err)
		return fmt.Errorf("phase 1 failed: %w", err)
	}

	// ========== Phase 2: ReAct explore Rich Context ==========
	if !a.sharedCtx.Quiet {
		fmt.Printf("\n[%s] Phase 2: Exploring rich context...\n", a.id)
	}
	if err := a.exploreRichContext(ctx); err != nil {
		return err
	}

	// Phase 3: Generate table description (from collected info)
	if !a.sharedCtx.Quiet {
		fmt.Printf("\n[%s] Phase 3: Generating table description...\n", a.id)
	}
	if err := a.generateTableDescription(ctx); err != nil {
	if !a.sharedCtx.Quiet {
		fmt.Printf("[%s] Warning: Failed to generate description: %v\n", a.id, err)
	}
		// Do not interrupt flow, description gen failure is non-fatal
	}

	// completes task
	a.sharedCtx.CompleteTask(a.taskID, map[string]interface{}{
		"table": a.tableName,
	})

	if !a.sharedCtx.Quiet {
		fmt.Printf("\n[%s] Analysis complete for '%s'\n", a.id, a.tableName)
	}
	return nil
}

// collectBasicMetadata Phase 1: collect basic metadata (fixed flow)
func (a *WorkerAgent) collectBasicMetadata(ctx context.Context) error {
	// Generate queries based on DB type
	var queries string
	switch a.adapter.GetDatabaseType() {
	case "MySQL":
		queries = fmt.Sprintf(`1. DESCRIBE %s
2. SHOW INDEX FROM %s
3. SELECT COUNT(*) FROM %s
4. SHOW CREATE TABLE %s`, a.tableName, a.tableName, a.tableName, a.tableName)
	case "PostgreSQL":
		queries = fmt.Sprintf(`1. SELECT column_name, data_type, is_nullable, column_default FROM information_schema.columns WHERE table_name='%s'
2. SELECT indexname, indexdef FROM pg_indexes WHERE tablename='%s'
3. SELECT COUNT(*) FROM %s
4. SELECT tc.constraint_name, kcu.column_name, ccu.table_name AS foreign_table_name, ccu.column_name AS foreign_column_name FROM information_schema.table_constraints AS tc JOIN information_schema.key_column_usage AS kcu ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema JOIN information_schema.constraint_column_usage AS ccu ON ccu.constraint_name = tc.constraint_name AND ccu.table_schema = tc.table_schema WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_name='%s'`, a.tableName, a.tableName, a.tableName, a.tableName)
	case "SQLite":
		queries = fmt.Sprintf(`1. PRAGMA table_info(%s)
2. PRAGMA index_list(%s)
3. SELECT COUNT(*) FROM %s
4. PRAGMA foreign_key_list(%s)`, a.tableName, a.tableName, a.tableName, a.tableName)
	default:
		queries = fmt.Sprintf(`1. DESCRIBE %s
2. SHOW INDEX FROM %s
3. SELECT COUNT(*) FROM %s
4. SHOW CREATE TABLE %s`, a.tableName, a.tableName, a.tableName, a.tableName)
	}

	prompt := fmt.Sprintf(`You are analyzing table "%s" in %s database.

Phase 1: Collect basic metadata using these EXACT queries:

%s

Execute these queries ONE BY ONE. After all queries complete, say "Phase 1 complete".`,
		a.tableName, a.adapter.GetDatabaseType(), queries)

	_, err := a.executor.Call(ctx, map[string]any{"input": prompt})
	if err != nil {
		return err
	}

	// Build basic metadata after Phase 1 completes
	a.sharedCtx.BuildTableMetadata(a.tableName)
	return nil
}

// exploreRichContext Phase 2: ReAct loop for business insights
func (a *WorkerAgent) exploreRichContext(ctx context.Context) error {
	// Get DB-type specific SQL syntax hints
	dbType := a.adapter.GetDatabaseType()
	sqlHint := ""
	switch dbType {
	case "SQLite":
		sqlHint = "Note: This is SQLite. Use PRAGMA table_info(table_name) instead of DESCRIBE."
	case "MySQL":
		sqlHint = "Note: This is MySQL. Use DESCRIBE table_name or SHOW COLUMNS FROM table_name."
	case "PostgreSQL":
		sqlHint = "Note: This is PostgreSQL. Use \\d table_name or query information_schema.columns."
	}

	prompt := fmt.Sprintf(`You are analyzing table "%s" in %s database.
%s

Phase 2: Discover RICH CONTEXT - Focus on **DATA QUALITY ISSUES** first, then business meaning.

**CRITICAL: You MUST check data quality issues for EVERY TEXT column systematically.**

MANDATORY WORKFLOW (follow this order strictly):

STEP 1: For EACH TEXT/VARCHAR column, check whitespace (MOST COMMON ISSUE):
Execute: SELECT [column] FROM %s WHERE [column] != TRIM([column]) LIMIT 3
- If returns ANY results: IMMEDIATELY save quality issue with ⚠️ prefix
- If empty: column is clean, continue to next column
- DO NOT SKIP any TEXT column

STEP 2: For EACH TEXT column, check if storing numbers:
Execute: SELECT [column] FROM %s WHERE [column] GLOB '*[0-9]*' LIMIT 10
- Inspect if values are purely numeric ("100", "200") vs mixed ("ABC123")
- If purely numeric: save type mismatch issue with ⚠️ prefix

STEP 3: For foreign key columns, check orphan records:
Execute: SELECT COUNT(*) FROM %s child LEFT JOIN [parent_table] parent ON child.[fk_column] = parent.[pk_column] WHERE parent.[pk_column] IS NULL
- IMPORTANT: Use correct primary key column name from parent table
- If count > 0: save orphan issue with ⚠️ prefix

STEP 4: After quality checks, record business meaning for key columns:
- Primary keys, foreign keys, important business fields
- Value distributions for small enumerations (<20 distinct values)

**QUALITY ISSUE NAMING CONVENTION:**
- Whitespace: "[column]_quality_issue" → "⚠️ Contains leading/trailing whitespace. Use TRIM([column]) for exact matching and joins."
- Type mismatch: "[column]_quality_issue" → "⚠️ TEXT field storing numeric values. Use CAST([column] AS INTEGER) for numeric operations."
- Orphan records: "[table]_orphan_issue" → "⚠️ Contains N orphan records ([fk] not in [parent]). Use LEFT JOIN to preserve all records."
- NULL/empty: "[column]_quality_issue" → specific percentage and meaning

Examples:

Type mismatch - TEXT storing numbers:
Action: execute_sql
Action Input: SELECT horsepower FROM cars_data WHERE horsepower IS NOT NULL LIMIT 10
Observation: ["100", "150", "200", "90", "", "N/A", "175"]
Action: set_rich_context
Action Input: horsepower_quality_issue|⚠️ TEXT field storing numeric values. Contains empty strings and 'N/A'. Requires CAST(horsepower AS INTEGER) for numeric operations. 15%% NULL.

Whitespace issue:
Action: execute_sql
Action Input: SELECT SourceAirport FROM flights WHERE SourceAirport != TRIM(SourceAirport) LIMIT 3
Observation: [" JFK", "LAX ", " ORD "]
Action: set_rich_context
Action Input: SourceAirport_quality_issue|⚠️ Contains leading/trailing whitespace. Use TRIM(SourceAirport) for exact matching and joins.

Orphan records:
Action: execute_sql
Action Input: SELECT COUNT(*) FROM model_list ml LEFT JOIN car_makers cm ON ml.Maker = cm.Id WHERE cm.Id IS NULL
Observation: 1 orphan record
Action: set_rich_context
Action Input: model_list_orphan_issue|⚠️ Contains 1 orphan record (Maker ID not in car_makers). Use LEFT JOIN to preserve all records.

NULL meaning:
Action: execute_sql
Action Input: SELECT COUNT(*), COUNT(price) FROM products
Observation: total=1000, non_null=850
Action: set_rich_context
Action Input: price_quality_issue|15%% NULL values, indicating price not yet set for new products.

Empty vs NULL:
Action: execute_sql
Action Input: SELECT COUNT(*) FROM users WHERE email = ''
Observation: 50 empty strings
Action: set_rich_context
Action Input: email_quality_issue|Contains 50 empty strings ('') in addition to NULLs. Check both: WHERE email IS NULL OR email = ''

Continue exploring. Say "Phase 2 complete" when done.`,
		a.tableName, dbType, sqlHint, a.tableName, a.tableName, a.tableName)

	_, err := a.executor.Call(ctx, map[string]any{"input": prompt})
	return err
}

// WorkerSQLTool SQL tool (for worker agent)
type WorkerSQLTool struct {
	adapter   adapter.DBAdapter
	sharedCtx *contextpkg.SharedContext
	agentID   string
	tableName string
}

func (t *WorkerSQLTool) Name() string {
	return "execute_sql"
}

func (t *WorkerSQLTool) Description() string {
	return `Execute SQL queries to analyze the table.

Use this to collect:
- Column information (schema, types, etc.)
- Index information: SHOW INDEX FROM table_name
- Row count: SELECT COUNT(*) FROM table_name

Execute queries one by one and collect all information.`
}

func (t *WorkerSQLTool) Call(ctx context.Context, input string) (string, error) {
	if !t.sharedCtx.Quiet {
		fmt.Printf("\n[%s] SQL: %s\n", t.agentID, input)
	}

	// Execute SQL
	result, err := t.adapter.ExecuteQuery(ctx, input)
	if err != nil {
		return "", err
	}

	if result.Error != "" {
		return fmt.Sprintf("SQL Error: %s", result.Error), nil
	}

	// Format results
	output := fmt.Sprintf("✓ Query successful! (%d rows, %dms)\n\n", result.RowCount, result.ExecutionTime)

	// Show results
	if result.RowCount > 0 {
		output += "Results:\n"
		jsonBytes, _ := json.MarshalIndent(result.Rows, "", "  ")
		output += string(jsonBytes) + "\n"
	}

	// Auto-save data to SharedContext
	queryType := detectQueryType(input)
	if queryType != "" {
		dataKey := fmt.Sprintf("%s_%s", t.tableName, queryType)
		t.sharedCtx.SetData(dataKey, result.Rows)
		output += fmt.Sprintf("\n💾 Data saved to context: %s\n", dataKey)
	}

	return output, nil
}

func detectQueryType(sql string) string {
	sql = strings.ToUpper(sql)

	// Column info query
	if strings.Contains(sql, "DESCRIBE") ||
		strings.Contains(sql, "PRAGMA TABLE_INFO") ||
		strings.Contains(sql, "INFORMATION_SCHEMA.COLUMNS") {
		return "columns"
	}

	// Index info query
	if strings.Contains(sql, "SHOW INDEX") ||
		strings.Contains(sql, "PRAGMA INDEX_LIST") ||
		strings.Contains(sql, "PG_INDEXES") {
		return "indexes"
	}

	// Row count query
	if strings.Contains(sql, "COUNT(*)") {
		return "rowcount"
	}

	// Foreign key info query
	if strings.Contains(sql, "FOREIGN_KEY_LIST") ||
		strings.Contains(sql, "SHOW CREATE TABLE") ||
		(strings.Contains(sql, "TABLE_CONSTRAINTS") && strings.Contains(sql, "FOREIGN KEY")) {
		return "foreignkeys"
	}

	return ""
}

// generateTableDescription generates table business description
func (a *WorkerAgent) generateTableDescription(ctx context.Context) error {
	// Get table metadata and Rich Context
	table, exists := a.sharedCtx.Tables[a.tableName]
	if !exists {
		// Table metadata may not have been built yet, skip description
	if !a.sharedCtx.Quiet {
		fmt.Printf("[%s] Warning: table %s not found in Tables map, skipping description\n", a.id, a.tableName)
	}
		return nil
	}

	// Build Prompt
	prompt := fmt.Sprintf(`You are a database expert. Based on the table metadata and business insights collected, generate a concise one-sentence description of this table's purpose.

Table: %s
Row Count: %d
Columns: %d

Business Insights:
`, a.tableName, table.RowCount, len(table.Columns))

	if len(table.RichContext) > 0 {
		for key, value := range table.RichContext {
			prompt += fmt.Sprintf("- %s: %s\n", key, value)
		}
	} else {
		prompt += "(No business insights collected yet)\n"
	}

	prompt += `
Task: Generate a single-sentence description that summarizes this table's business purpose.
Output format: Just the description sentence, no extra text.

Description:`

	// Call LLM
	response, err := a.llm.Call(ctx, prompt)
	if err != nil {
		return err
	}

	description := strings.TrimSpace(response)

	// Save description
	if err := a.sharedCtx.SetTableDescription(a.tableName, description); err != nil {
		return err
	}

	if !a.sharedCtx.Quiet {
		fmt.Printf("[%s] Generated description: %s\n", a.id, description)
	}
	return nil
}

// SetRichContextTool tool for setting Rich Context
type SetRichContextTool struct {
	sharedCtx *contextpkg.SharedContext
	agentID   string
	tableName string
}

func (t *SetRichContextTool) Name() string {
	return "set_rich_context"
}

func (t *SetRichContextTool) Description() string {
	return `Save business insights and DATA QUALITY ISSUES to rich context. Use this IMMEDIATELY after discovering insights.

Input format: key|value

Key naming conventions:
- Business insights: {column}_values, {column}_meaning, business_rules
- Quality issues: {column}_quality_issue (CRITICAL for SQL generation)

Value: ONLY the insight itself, NO Thought/Action/Observation text.

Good examples:
- status_values|0=disabled(10%), 1=active(90%)
- business_rules|dept_id=0 means unassigned department
- payment_methods|1=Alipay(50%), 2=WeChat(30%), 3=Bank(20%)
- horsepower_quality_issue|⚠️ TEXT field storing numeric values. Requires CAST() for comparisons.
- airport_code_quality_issue|⚠️ Contains whitespace. Use TRIM() for exact matching.
- model_list_orphan_issue|⚠️ 1 orphan record (Maker ID not in car_makers).

Bad examples (DO NOT include Thought/Action):
- status_values|0=disabled(10%), 1=active(90%)\n\nThought: Next I will...

IMPORTANT: 
- Save insights IMMEDIATELY after each discovery, not at the end
- Use ⚠️ prefix for quality issues to highlight them
- Quality issues are CRITICAL - they directly affect SQL query correctness`
}

func (t *SetRichContextTool) Call(ctx context.Context, input string) (string, error) {
	// Parse input: key|value
	parts := strings.SplitN(input, "|", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid format, expected: key|value")
	}

	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])

	// Clean value: remove Thought/Action/Observation text
	// Find newline followed by Thought: or Action:
	if idx := strings.Index(value, "\n\nThought:"); idx > 0 {
		value = value[:idx]
	}
	if idx := strings.Index(value, "\n\nAction:"); idx > 0 {
		value = value[:idx]
	}
	if idx := strings.Index(value, "\nThought:"); idx > 0 {
		value = value[:idx]
	}
	if idx := strings.Index(value, "\nAction:"); idx > 0 {
		value = value[:idx]
	}

	value = strings.TrimSpace(value)

	if key == "" || value == "" {
		return "", fmt.Errorf("key and value cannot be empty")
	}

	// Save to SharedContext (7-day expiry)
	expiresAt := time.Now().Add(7 * 24 * time.Hour).Format(time.RFC3339)
	err := t.sharedCtx.SetTableRichContext(t.tableName, key, value, expiresAt)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("✓ Rich context saved: %s = %s", key, value), nil
}
