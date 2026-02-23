package inference

import (
	"context"
	"fmt"
	"strings"
	"time"

	"reactsql/internal/adapter"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/tools"
)

// oneShotGeneration one-shot SQL generation
func (p *Pipeline) oneShotGeneration(ctx context.Context, query string, contextPrompt string, crossTableSummary string) (string, error) {
	prompt := p.buildPrompt(query, contextPrompt, crossTableSummary, false)

	p.Logger.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	p.Logger.Println(" SQL Generation (One-shot) - Prompt to LLM:")
	p.Logger.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	p.Logger.Println(prompt)
	p.Logger.Println()

	// Call LLM with backoff retry
	var response string
	var err error
	maxRetries := 2
	backoffDelays := []time.Duration{1 * time.Second, 3 * time.Second}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		response, err = p.llm.Call(ctx, prompt)
		if err == nil {
			break
		}

		// If retries left, wait and retry
		if attempt < maxRetries {
			delay := backoffDelays[attempt]
		p.Logger.Printf("⚠️  SQL Generation failed (attempt %d/%d): %v\n", attempt+1, maxRetries+1, err)
			p.Logger.Printf("⏳ Retrying after %v...\n\n", delay)
			time.Sleep(delay)
		}
	}

	if err != nil {
		return "", fmt.Errorf("LLM call failed after %d attempts: %w", maxRetries+1, err)
	}

	// Record tokens
	p.promptTexts = append(p.promptTexts, prompt)
	p.responseTexts = append(p.responseTexts, response)

	p.Logger.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	p.Logger.Println("💡 SQL Generation - LLM Response:")
	p.Logger.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	p.Logger.Println(response)
	p.Logger.Println()

	// Extract SQL
	sql := p.extractSQL(response)

	p.Logger.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	p.Logger.Println(" Extracted SQL:")
	p.Logger.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	p.Logger.Println(sql)
	p.Logger.Println()

	return sql, nil
}

// reactLoop ReAct loop
func (p *Pipeline) reactLoop(ctx context.Context, query string, contextPrompt string, crossTableSummary string, result *Result) (string, error) {
	// Create tools
	sqlTool := &SQLTool{
		adapter:   p.adapter,
		useDryRun: p.config.UseDryRun,
		logger:    p.Logger,
	}

	clarifyTool := &ClarifyTool{
		resultFields:            p.config.ResultFields,
		resultFieldsDescription: p.config.ResultFieldsDescription,
		logger:                  p.Logger,
	}

	// Create verify_sql tool
	verifySQLTool := NewVerifySQLTool(p.adapter, p.config.DBType)
	verifySQLTool.logger = p.Logger

	// Create ReAct Agent
	var toolsList []tools.Tool
	toolsList = []tools.Tool{sqlTool, verifySQLTool}

	if p.config.ClarifyMode == "on" {
		toolsList = append(toolsList, clarifyTool)
	}

	if p.config.EnableProofread {
		updateTool := NewUpdateRichContextTool(p.config.DBName, p.config.DBType)
		updateTool.logger = p.Logger
		toolsList = append(toolsList, updateTool)
	}

	// Create handler to collect ReAct steps
	reactHandler := &PrettyReActHandler{logMode: p.config.LogMode, logger: p.Logger}

	// Set up streaming callback if available (for real-time step notifications)
	if p.stepCallback != nil {
		reactHandler.SetStepNotifier(func(step CollectedStep, eventType string) {
			p.stepCallback(ReActStep{
				Step:        step.Step,
				Thought:     step.Thought,
				Action:      step.Action,
				ActionInput: step.ActionInput,
				Observation: step.Observation,
				Phase:       "sql_generation",
			}, eventType)
		})
	}

	// Use higher actual iterations than what we show in prompt
	// This gives the model more chances to complete while not overwhelming the prompt
	actualMaxIterations := p.config.MaxIterations * 4 // e.g., user sets 5, we allow 20
	claimedMaxIterations := p.config.MaxIterations     // what we tell the model

	executor, err := agents.Initialize(
		p.llm,
		toolsList,
		agents.ZeroShotReactDescription,
		agents.WithMaxIterations(actualMaxIterations),
		agents.WithCallbacksHandler(reactHandler),
	)
	if err != nil {
		return "", err
	}

	// Build Prompt - pass claimed iterations to prompt
	prompt := p.buildPrompt(query, contextPrompt, crossTableSummary, true)

	// Print key info only, skip full prompt（avoid duplicate Best Practices etc.）
	p.Logger.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	p.Logger.Printf("🔄 Starting ReAct Loop (Claimed %d, Actual Max %d iterations)\n", claimedMaxIterations, actualMaxIterations)
	p.Logger.Printf("Question: %s\n", query)
	p.Logger.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	agentResult, err := executor.Call(ctx, map[string]any{"input": prompt})
	if err != nil {
		p.Logger.Printf("\n❌ ReAct Loop failed: %v\n\n", err)
		return "", err
	}

	p.Logger.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	p.Logger.Println("✅ ReAct Loop completed successfully")
	p.Logger.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// Collect ReAct steps from handler
	collectedSteps := reactHandler.GetCollectedSteps()
	for _, step := range collectedSteps {
		result.ReActSteps = append(result.ReActSteps, ReActStep{
			Thought:     step.Thought,
			Action:      step.Action,
			ActionInput: step.ActionInput,
			Observation: step.Observation,
			Phase:       "sql_generation",
		})
	}

	// Update statistics
	result.LLMCalls += len(collectedSteps) // Use actual iteration count
	result.SQLExecutions += sqlTool.ExecutionCount
	result.ClarifyCount = clarifyTool.ClarifyCount

	// Extract final SQL
	if output, ok := agentResult["output"].(string); ok {
		sql := p.extractSQL(output)
		return sql, nil
	}

	return "", fmt.Errorf("no SQL generated")
}

// buildPrompt builds prompt
func (p *Pipeline) buildPrompt(query string, contextPrompt string, crossTableSummary string, isReact bool) string {
	var sb strings.Builder

	sb.WriteString("You are a SQL expert. Generate SQL to answer the question.\n\n")

	// Database type info
	if p.config.DBType != "" {
		sb.WriteString(fmt.Sprintf("**Database Type: %s**\n", p.config.DBType))
		sb.WriteString(fmt.Sprintf("CRITICAL: Write SQL that strictly follows %s syntax rules.\n", p.config.DBType))
		sb.WriteString("Common syntax differences to watch:\n")
		switch p.config.DBType {
		case "SQLite":
			sb.WriteString("- Use double quotes for identifiers if needed, single quotes for strings\n")
			sb.WriteString("- No LIMIT offset without LIMIT clause\n")
			sb.WriteString("- Use || for string concatenation\n")
		case "MySQL":
			sb.WriteString("- Use backticks for identifiers, single quotes for strings\n")
			sb.WriteString("- LIMIT syntax: LIMIT offset, count\n")
			sb.WriteString("- Use CONCAT() for string concatenation\n")
		case "PostgreSQL":
			sb.WriteString("- Use double quotes for identifiers, single quotes for strings\n")
			sb.WriteString("- LIMIT syntax: LIMIT count OFFSET offset\n")
			sb.WriteString("- Use || for string concatenation\n")
		}
		sb.WriteString("\n")
	}

	// Rich Context
	if contextPrompt != "" {
		sb.WriteString("Database Schema:\n")
		sb.WriteString(contextPrompt)
		sb.WriteString("\n\n")
	}

	// Cross-table quality summary (smart injection from full-table analysis)
	if crossTableSummary != "" {
		sb.WriteString(crossTableSummary)
		sb.WriteString("\n")
	}

	// SQL Best Practices (only added with Rich Context)
	// These are enhanced hints from onboarding, should not be used in baseline
	if p.config.UseRichContext {
		// JOIN paths and field semantics (only in Rich Context mode)
		if p.context != nil {
			if joinPathsPrompt := p.context.FormatJoinPathsForPrompt(); joinPathsPrompt != "" {
				sb.WriteString(joinPathsPrompt)
			}
			if fieldSemanticsPrompt := p.context.FormatFieldSemanticsForPrompt(); fieldSemanticsPrompt != "" {
				sb.WriteString(fieldSemanticsPrompt)
			}
		}

		sb.WriteString("IMPORTANT: Rich Context may be outdated or incorrect. When Rich Context conflicts with actual database data, trust the database.\n\n")

		if p.config.Benchmark == "bird" {
			sb.WriteString(p.buildBirdBestPractices())
		} else {
			sb.WriteString(p.buildSpiderBestPractices())
		}
	}

	sb.WriteString(fmt.Sprintf("Question: %s\n\n", query))

	// force mode: mandatory field info in prompt
	if p.config.ClarifyMode == "force" && len(p.config.ResultFields) > 0 {
		sb.WriteString("⚠️ REQUIRED OUTPUT FIELDS:\n")
		fieldsStr := strings.Join(p.config.ResultFields, ", ")
		sb.WriteString(fmt.Sprintf("Your SQL query MUST return EXACTLY these fields in this EXACT ORDER: %s\n", fieldsStr))
		if p.config.ResultFieldsDescription != "" {
			sb.WriteString(fmt.Sprintf("Field descriptions: %s\n", p.config.ResultFieldsDescription))
		}
		sb.WriteString("\nCRITICAL: Use these field names WITHOUT table prefixes (e.g., 'Name' not 'singer.Name').\n")
		sb.WriteString("Any deviation from this field list will be considered INCORRECT.\n\n")
	}

	if isReact {
		// Tools available
		sb.WriteString(`Available Tools:
- execute_sql: Execute SQL and see results`)
		if p.config.ClarifyMode == "on" {
			sb.WriteString(`
- clarify_fields: Ask which fields to return (when question doesn't specify)`)
		}
		if p.config.EnableProofread {
			sb.WriteString(`
- update_rich_context: Update expired/incorrect Rich Context`)
		}

		// Workflow
		sb.WriteString(`

Workflow:
1. Analyze question and schema`)
		if p.config.ClarifyMode == "on" {
			sb.WriteString(`
2. If unclear which columns needed → use clarify_fields
3. If string values missing from Rich Context → use execute_sql to find them`)
		} else {
			sb.WriteString(`
2. If string values missing from Rich Context → use execute_sql to find them`)
		}
		if p.config.EnableProofread {
			sb.WriteString(`
4. If Rich Context conflicts with actual data → use update_rich_context`)
		}
		sb.WriteString(`
5. Write SQL following best practices
6. If uncertain → validate with execute_sql (use LIMIT/COUNT for large results)
7. Provide Final Answer

`)

		// Output format
		sb.WriteString(`Output Format (choose ONE):
A) Use tool:
   Thought: [reasoning]
   Action: [tool_name]
   Action Input: [input]

B) Give answer:
   Thought: [reasoning]
   Final Answer: [SQL only, no markdown]

⚠️ NEVER write "Action: None"! If no tool needed, use option B.

`)

		// Critical rules
		sb.WriteString(`Critical Rules:
1. Field Order: SELECT fields MUST match expected order exactly (no table prefixes)
2. Iterations: 5 max (update_rich_context doesn't count). Track: "Iteration X/5"
3. Efficiency: Only use execute_sql when truly uncertain
4. No repetition: If stuck, try different approach
5. Final Answer: SQL only, no explanations

`)

		// In ReAct mode, re-emphasize field requirements (prevent long-range attention loss)
		if p.config.ClarifyMode == "force" && len(p.config.ResultFields) > 0 {
			sb.WriteString(`
⚠️ REMINDER - REQUIRED OUTPUT FIELDS ⚠️
Before Final Answer, verify your SQL returns these EXACT fields in EXACT order:
`)
			fieldsStr := strings.Join(p.config.ResultFields, ", ")
			sb.WriteString(fmt.Sprintf("Required: %s\n", fieldsStr))
			if p.config.ResultFieldsDescription != "" {
				sb.WriteString(fmt.Sprintf("(%s)\n", p.config.ResultFieldsDescription))
			}
			sb.WriteString(`If field is a name/description, JOIN the referenced table. Do NOT return IDs when names are required.
`)
		}

		if p.config.ClarifyMode == "on" {
			sb.WriteString(`
6. Clarify: Follow field names/descriptions from clarify_fields precisely
`)
		}
	} else {
		sb.WriteString(`Task: Generate SQL directly.
Output ONLY the SQL query (no explanations, no markdown).

Format:
SELECT ...`)
	}

	return sb.String()
}

// extractSQL extracts SQL from response
func (p *Pipeline) extractSQL(response string) string {
	// Try extracting Final Answer
	if idx := strings.Index(response, "Final Answer:"); idx >= 0 {
		response = response[idx+13:]
	}

	// Clean up
	response = strings.TrimSpace(response)

	// Remove markdown code blocks
	response = strings.TrimPrefix(response, "```sql")
	response = strings.TrimPrefix(response, "```")
	response = strings.TrimSuffix(response, "```")
	response = strings.TrimSpace(response)

	// Extract backtick-wrapped SQL
	if strings.Contains(response, "`SELECT") || strings.Contains(response, "`select") {
		start := strings.Index(response, "`")
		if start >= 0 {
			end := strings.Index(response[start+1:], "`")
			if end >= 0 {
				response = response[start+1 : start+1+end]
			}
		}
	}

	// If multi-line response，and first line is SELECT, take first line only
	lines := strings.Split(response, "\n")
	if len(lines) > 1 {
		firstLine := strings.TrimSpace(lines[0])
		if strings.HasPrefix(strings.ToUpper(firstLine), "SELECT") ||
			strings.HasPrefix(strings.ToUpper(firstLine), "WITH") ||
			strings.HasPrefix(strings.ToUpper(firstLine), "INSERT") ||
			strings.HasPrefix(strings.ToUpper(firstLine), "UPDATE") ||
			strings.HasPrefix(strings.ToUpper(firstLine), "DELETE") {
			// Find SQL statement end (non-SQL content)
			var sqlLines []string
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				// If explanatory text encountered (e.g. "This query"), stop
				if strings.HasPrefix(trimmed, "This ") ||
					strings.HasPrefix(trimmed, "The ") ||
					strings.HasPrefix(trimmed, "Since ") ||
					strings.HasPrefix(trimmed, "Note:") {
					break
				}
				sqlLines = append(sqlLines, line)
			}
			response = strings.Join(sqlLines, "\n")
		}
	}

	return strings.TrimSpace(response)
}

// SQLTool SQL execution tool
type SQLTool struct {
	adapter        adapter.DBAdapter
	useDryRun      bool
	ExecutionCount int
	logger         *InferenceLogger
}

func (t *SQLTool) Name() string {
	return "execute_sql"
}

func (t *SQLTool) Description() string {
	if t.useDryRun {
		return `Execute SQL query with dry run validation first.
Input: SQL query string
Output: Query results or validation errors`
	}
	return `Execute SQL query and return results.
Input: SQL query string
Output: Query results`
}

func (t *SQLTool) Call(ctx context.Context, input string) (string, error) {
	t.ExecutionCount++

	logf := func(format string, a ...interface{}) {
		if t.logger != nil {
			t.logger.Printf(format, a...)
		} else {
			fmt.Printf(format, a...)
		}
	}

	logf("\n🔧 Tool Call [execute_sql] #%d:\n", t.ExecutionCount)
	logf("Input SQL: %s\n", input)

	sql := strings.TrimSpace(input)

	// Dry Run validation (if enabled)
	if t.useDryRun {
		if err := t.adapter.DryRunSQL(ctx, sql); err != nil {
			return fmt.Sprintf("SQL validation failed: %v", err), nil
		}
	}

	// Execute SQL
	result, err := t.adapter.ExecuteQuery(ctx, sql)
	if err != nil {
		return fmt.Sprintf("SQL execution failed: %v", err), nil
	}

	// Format results
	output := fmt.Sprintf("Query executed successfully!\nRows: %d\n", result.RowCount)

	// Decide display based on char length not row count
	// Serialize result and check length
	if result.RowCount > 0 {
		sampleStr := fmt.Sprintf("%v", result.Rows)
		const maxSampleLength = 1000 // max display 1000 chars

		if len(sampleStr) <= maxSampleLength {
			// Full display
			output += fmt.Sprintf("Sample results: %s\n", sampleStr)
		} else {
			// Truncate with ellipsis
			truncated := sampleStr[:maxSampleLength]
			output += fmt.Sprintf("Sample results: %s... (truncated, showing first %d chars of %d total)\n",
				truncated, maxSampleLength, len(sampleStr))
		}
	}

	logf("Output: %s\n", output)

	return output, nil
}

// ClarifyTool tool for asking which fields to return
type ClarifyTool struct {
	resultFields            []string
	resultFieldsDescription string
	ClarifyCount            int
	logger                  *InferenceLogger
}

func (t *ClarifyTool) Name() string {
	return "clarify_fields"
}

func (t *ClarifyTool) Description() string {
	return `Ask for clarification about which fields should be returned in the query result.
Use this when the question doesn't specify which columns to return.
Input: Your question about which fields to return (e.g., "Which fields should I return?")
Output: List of required fields or description of required fields`
}

func (t *ClarifyTool) Call(ctx context.Context, input string) (string, error) {
	t.ClarifyCount++

	logf := func(format string, a ...interface{}) {
		if t.logger != nil {
			t.logger.Printf(format, a...)
		} else {
			fmt.Printf(format, a...)
		}
	}

	logf("\n🔔 Clarification requested: %s\n", input)

	// Return field list + descriptions
	fieldsStr := strings.Join(t.resultFields, ", ")
	response := fmt.Sprintf("Required fields in EXACT ORDER: %s\n\nField descriptions: %s\n\nIMPORTANT: Use these field names WITHOUT table prefixes (e.g., 'Name' not 'singer.Name')",
		fieldsStr,
		t.resultFieldsDescription)

	logf("📋 Clarification response: %s\n\n", response)

	return response, nil
}

// buildSpiderBestPractices returns SQL best practices for Spider benchmark
func (p *Pipeline) buildSpiderBestPractices() string {
	return `SQL Rules & Best Practices:
1. Type Mismatch (TEXT storing numbers):
   - Use CAST(field AS INTEGER/REAL) for comparisons, sorting, and aggregation
   - Filter non-numeric values: WHERE field IS NOT NULL AND field != '' AND field != 'null'
2. Whitespace: If quality issues mention whitespace, use TRIM(field) in JOIN/WHERE/GROUP BY
3. NULL handling: NULL ≠ 0. Filter with IS NOT NULL. For TEXT fields, also check field != ''
4. String matching:
   - Use exact values from Rich Context when available
   - In ReAct mode: use execute_sql to find exact values when uncertain
5. Aggregation patterns:
   - "Highest/Lowest/Top N": ORDER BY col DESC/ASC LIMIT N (NOT MAX/MIN which returns 1 row)
   - "Count by X": SELECT X, COUNT(*) ... GROUP BY X (MUST include GROUP BY)
   - "Rate/Percentage": CAST(num AS REAL) / CAST(denom AS REAL) (avoid integer division)
6. Extreme values with ties:
   - Use subquery: WHERE col = (SELECT MAX/MIN(col) FROM table)
   - AVOID ORDER BY + LIMIT 1 (misses ties)
   - Exception: question says "one" or "any one" → LIMIT 1 is OK
7. Duplicates: When listing items from JOINs, consider DISTINCT to avoid duplicates
8. Orphan records: If quality issues mention orphans, use LEFT JOIN instead of INNER JOIN
9. Value verification: When using specific text values in WHERE, verify which column contains it first

`
}

// buildBirdBestPractices returns SQL best practices tailored for BIRD benchmark
// BIRD-specific: evidence-driven, projection-focused, DISTINCT-aware
func (p *Pipeline) buildBirdBestPractices() string {
	return `SQL Rules & Best Practices (BIRD):
1. EVIDENCE IS CRITICAL: The "Evidence" section contains exact column mappings, value constraints, and formulas.
   - If evidence says "X refers to Y = 'Z'" → you MUST use column Y with value 'Z'
   - If evidence gives a formula (e.g., "percentage = DIVIDE(A, B)") → use that exact formula
   - If evidence defines a threshold (e.g., "normal range refers to X > 900 AND X < 2000") → use those exact bounds
   - NEVER ignore or reinterpret evidence constraints
2. Projection (SELECT columns):
   - Return ONLY the columns the question asks for — no extra columns
   - If question asks "what is X" → SELECT X only, not X plus other info
   - Match the gold column count: 1 question = 1 column unless explicitly multi-column
   - When question asks for a name/description, JOIN to get the text — do NOT return IDs
3. DISTINCT usage:
   - Use DISTINCT only when the question says "different", "unique", "distinct", or when JOINs produce actual duplicates
   - Do NOT add DISTINCT by default — many queries expect duplicate rows
   - If unsure, run the query first and check for unintended duplicates
4. Type Mismatch: Use CAST(field AS INTEGER/REAL) for TEXT columns storing numbers
5. Percentage/Rate: Always use CAST(... AS REAL) to avoid integer division truncation
6. IIF/CASE patterns: For yes/no or conditional results, use IIF(condition, 'YES', 'NO') or CASE WHEN
7. Aggregation:
   - "Highest/Top N": ORDER BY col DESC LIMIT N
   - "Rate/Percentage": CAST(numerator AS REAL) * 100 / denominator
8. NULL handling: NULL ≠ 0 ≠ ''. Filter with IS NOT NULL, and for TEXT also check != ''
9. Date handling: Use substr(date, 1, 10) or LIKE 'YYYY-MM-DD%' for date prefix matching

`
}

