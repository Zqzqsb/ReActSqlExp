package inference

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/tools"

	"reactsql/internal/adapter"
	contextpkg "reactsql/internal/context"
)

// SchemaLinkResult holds the output of Schema Linking
type SchemaLinkResult struct {
	Tables       []string   // Selected table names
	Steps        []ReActStep // ReAct steps (if using ReAct mode)
	ContextPrompt string    // LLM-generated focused context for SQL generation (empty if not available)
}

// SchemaLinker module interface
type SchemaLinker interface {
	// Link performs Schema Linking
	// Input: query, all table info, optional full RC prompt
	// Output: SchemaLinkResult containing tables, steps, and optionally a focused context
	Link(ctx context.Context, query string, allTables map[string]*TableInfo, fullRCPrompt string) (*SchemaLinkResult, error)
}

// TableInfo brief table info (for Schema Linking)
type TableInfo struct {
	Name        string
	Columns     []string                        // Column name list
	ForeignKeys []contextpkg.ForeignKeyMetadata // Foreign key relationships
	Description string                          // Table description (optional, from rich_context or table comment)
	QualitySummary string                       // One-line quality issues summary
}

// LLMSchemaLinker LLM-based Schema Linking
type LLMSchemaLinker struct {
	llm           llms.Model
	adapter       adapter.DBAdapter
	useReact      bool
	tokenRecorder func(prompt, response string)
	logger        *InferenceLogger
}

// NewLLMSchemaLinker creates LLM Schema Linker
func NewLLMSchemaLinker(llm llms.Model, dbAdapter adapter.DBAdapter, useReact bool) *LLMSchemaLinker {
	return &LLMSchemaLinker{
		llm:      llm,
		adapter:  dbAdapter,
		useReact: useReact,
	}
}

// Link performs Schema Linking
func (l *LLMSchemaLinker) Link(ctx context.Context, query string, allTables map[string]*TableInfo, fullRCPrompt string) (*SchemaLinkResult, error) {
	if l.useReact {
		return l.linkWithReact(ctx, query, allTables, fullRCPrompt)
	}
	return l.linkOneShot(ctx, query, allTables, fullRCPrompt)
}

// linkOneShot One-shot Schema Linking
func (l *LLMSchemaLinker) linkOneShot(ctx context.Context, query string, allTables map[string]*TableInfo, fullRCPrompt string) (*SchemaLinkResult, error) {
	// Build table info description (formatted as readable list, include FK info)
	var schemaDesc strings.Builder
	for _, table := range allTables {
		schemaDesc.WriteString(fmt.Sprintf("- %s\n", table.Name))
		schemaDesc.WriteString(fmt.Sprintf("  Columns: %s\n", strings.Join(table.Columns, ", ")))
		if len(table.ForeignKeys) > 0 {
			schemaDesc.WriteString("  Foreign Keys:\n")
			for _, fk := range table.ForeignKeys {
				schemaDesc.WriteString(fmt.Sprintf("    %s → %s.%s\n", fk.ColumnName, fk.ReferencedTable, fk.ReferencedColumn))
			}
		}
		if table.Description != "" {
			schemaDesc.WriteString(fmt.Sprintf("  Description: %s\n", table.Description))
		}
		if table.QualitySummary != "" {
			schemaDesc.WriteString(fmt.Sprintf("  %s\n", table.QualitySummary))
		}
		schemaDesc.WriteString("\n")
	}

	// Build Prompt
	prompt := fmt.Sprintf(`You are a database expert. Identify which tables are relevant to answer the question.

Available Tables:
%s

Question: %s

Task: Select ALL tables needed to answer this question, including intermediate/bridge tables for JOINs.
IMPORTANT: If table A references table B via foreign key, and you need data from A, you likely need B too.
When in doubt, INCLUDE the table — it's better to select extra tables than to miss one.
Output format: table1, table2, table3 (comma-separated, no extra text)
If all tables are needed, output: all
If no tables are needed, output: none

Output:`, schemaDesc.String(), query)

	// Print summary to stdout + dump full prompt to log file
	if l.logger != nil {
		l.logger.Println("🔍 Schema Linking (One-shot)...")
		l.logger.FileOnly("\n┌─ Schema Linking Prompt ──────────────────────────────────\n")
		l.logger.FileOnly("%s", prompt)
		l.logger.FileOnly("└──────────────────────────────────────────────────────────\n\n")
	} else {
		fmt.Println("🔍 Schema Linking (One-shot)...")
	}

	// Call LLM with backoff retry
	var response string
	var err error
	maxRetries := 2
	backoffDelays := []time.Duration{1 * time.Second, 3 * time.Second}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		response, err = l.llm.Call(ctx, prompt)
		if err == nil {
			break
		}

		// If retries left, wait and retry
		if attempt < maxRetries {
			delay := backoffDelays[attempt]
			if l.logger != nil {
				l.logger.Printf("⚠️  Schema Linking failed (attempt %d/%d): %v\n", attempt+1, maxRetries+1, err)
				l.logger.Printf("⏳ Retrying after %v...\n\n", delay)
			} else {
				fmt.Printf("⚠️  Schema Linking failed (attempt %d/%d): %v\n", attempt+1, maxRetries+1, err)
				fmt.Printf("⏳ Retrying after %v...\n\n", delay)
			}
			time.Sleep(delay)
		}
	}

	if err != nil {
		return nil, fmt.Errorf("schema linking failed after %d attempts: %w", maxRetries+1, err)
	}

	response = strings.TrimSpace(response)

	// Log LLM response to file
	if l.logger != nil {
		l.logger.FileOnly("┌─ Schema Linking Response ────────────────────────────────\n")
		l.logger.FileOnly("%s\n", response)
		l.logger.FileOnly("└──────────────────────────────────────────────────────────\n\n")
	}

	// Record tokens
	if l.tokenRecorder != nil {
		l.tokenRecorder(prompt, response)
	}

	// Parse response
	if response == "all" {
		result := make([]string, 0, len(allTables))
		for name := range allTables {
			result = append(result, name)
		}
		tablesStr := strings.Join(result, ", ")
		steps := []ReActStep{
			{
				Thought: fmt.Sprintf("The question '%s' requires all tables to answer.", query),
				Action:  "final_answer",
				ActionInput: map[string]interface{}{
					"tables": tablesStr,
				},
				Observation: fmt.Sprintf("Selected tables: %s", tablesStr),
				Phase:       "schema_linking",
			},
		}
		return &SchemaLinkResult{Tables: result, Steps: steps}, nil
	}

	if response == "none" {
		steps := []ReActStep{
			{
				Thought: fmt.Sprintf("The question '%s' does not require any tables to answer.", query),
				Action:  "final_answer",
				ActionInput: map[string]interface{}{
					"tables": "none",
				},
				Observation: "No tables needed",
				Phase:       "schema_linking",
			},
		}
		return &SchemaLinkResult{Tables: []string{}, Steps: steps}, nil
	}

	// Take first line only (LLM may include extra explanation)
	lines := strings.Split(response, "\n")
	firstLine := strings.TrimSpace(lines[0])

	// Parse table name list
	tables := strings.Split(firstLine, ",")
	result := make([]string, 0, len(tables))
	for _, table := range tables {
		table = strings.TrimSpace(table)
		if table != "" {
			result = append(result, table)
		}
	}

	// Auto-complete: add FK-referenced tables that were missed
	result = l.autoCompleteFKTables(result, allTables)

	// Safety net: if DB is small (≤6 tables) and LLM selected very few, include all
	if len(allTables) <= 6 && len(result) < len(allTables) && len(result) <= 2 {
		result = make([]string, 0, len(allTables))
		for name := range allTables {
			result = append(result, name)
		}
		if l.logger != nil {
			l.logger.Printf("📋 Schema Linking: small DB safety net — included all %d tables\n", len(result))
		}
	}

	// Create a simple step to represent Schema Linking process
	tablesStr := strings.Join(result, ", ")
	steps := []ReActStep{
		{
			Thought: fmt.Sprintf("Analyzed the question '%s' and identified relevant tables based on their columns and descriptions.", query),
			Action:  "final_answer",
			ActionInput: map[string]interface{}{
				"tables": tablesStr,
			},
			Observation: fmt.Sprintf("Selected tables: %s", tablesStr),
			Phase:       "schema_linking",
		},
	}

	return &SchemaLinkResult{Tables: result, Steps: steps}, nil
}

// linkWithReact ReAct mode Schema Linking
func (l *LLMSchemaLinker) linkWithReact(ctx context.Context, query string, allTables map[string]*TableInfo, fullRCPrompt string) (*SchemaLinkResult, error) {
	if l.logger != nil {
		l.logger.Println("🔍 Schema Linking (ReAct mode)...")
	} else {
		fmt.Println("🔍 Schema Linking (ReAct mode)...")
	}

	// Create SQL tool
	sqlTool := &SQLTool{
		adapter:   l.adapter,
		useDryRun: false,
		logger:    l.logger,
	}

	// Create handler to collect ReAct steps
	reactHandler := &PrettyReActHandler{logMode: "simple", logger: l.logger}

	// Create ReAct Agent
	// Strategy: tell model max 5 iterations (urgency), actual 15 (enough room)
	actualMaxIterations := 15
	claimedMaxIterations := 5

	executor, err := agents.Initialize(
		l.llm,
		[]tools.Tool{sqlTool},
		agents.ZeroShotReactDescription,
		agents.WithMaxIterations(actualMaxIterations),
		agents.WithCallbacksHandler(reactHandler),
	)
	if err != nil {
		return nil, err
	}

	// Build schema description: use full RC if available, otherwise use basic table info
	var schemaSection string
	if fullRCPrompt != "" {
		schemaSection = fullRCPrompt
	} else {
		var schemaDesc strings.Builder
		for _, table := range allTables {
			schemaDesc.WriteString(fmt.Sprintf("- %s\n", table.Name))
			schemaDesc.WriteString(fmt.Sprintf("  Columns: %s\n", strings.Join(table.Columns, ", ")))
			if len(table.ForeignKeys) > 0 {
				schemaDesc.WriteString("  Foreign Keys:\n")
				for _, fk := range table.ForeignKeys {
					schemaDesc.WriteString(fmt.Sprintf("    %s → %s.%s\n", fk.ColumnName, fk.ReferencedTable, fk.ReferencedColumn))
				}
			}
			if table.Description != "" {
				schemaDesc.WriteString(fmt.Sprintf("  Description: %s\n", table.Description))
			}
			if table.QualitySummary != "" {
				schemaDesc.WriteString(fmt.Sprintf("  %s\n", table.QualitySummary))
			}
			schemaDesc.WriteString("\n")
		}
		schemaSection = schemaDesc.String()
	}

	// Build Prompt — with full RC, linker outputs BOTH tables AND focused context
	prompt := fmt.Sprintf(`You are a database expert. Your task has TWO parts:
1. Identify which tables (and columns) are relevant to the question.
2. Output a FOCUSED schema context containing ONLY the information needed for SQL generation.

⚠️  ITERATION LIMIT: You have maximum %d iterations. Be efficient!

Full Database Schema:
%s

Question: %s

You can use execute_sql to:
- Verify data existence: SELECT COUNT(*) FROM table
- Check column values: SELECT DISTINCT column FROM table LIMIT 5
- Explore sample data: SELECT * FROM table LIMIT 3

Workflow:
1. Read the schema and identify tables/columns relevant to the question.
2. If unsure about column values or table relevance, use execute_sql to verify.
3. Output the final answer in the EXACT format below.

IMPORTANT: Each iteration can only perform ONE action. Do NOT output multiple actions in a single response.

To explore data:
   Thought: [reasoning]
   Action: execute_sql
   Action Input: [single SQL query]

To give final answer (MUST follow this exact format):
   Thought: [reasoning]
   Final Answer:
   TABLES: table1, table2
   CONTEXT:
   [Write a focused schema description. Include ONLY:]
   [- Tables and columns needed for the query]
   [- Column types, PK/FK markers]
   [- Value statistics for columns referenced in WHERE/JOIN conditions]
   [- Data quality warnings ONLY for columns used in the query]

Example Final Answer:
   Final Answer:
   TABLES: orders, customers
   CONTEXT:
   Table orders (50000 rows):
     - order_id: INTEGER [PK]
     - customer_id: INTEGER → customers.customer_id
     - order_date: DATE
     - total_amount: REAL range=[0..9999]
   Table customers (1000 rows):
     - customer_id: INTEGER [PK]
     - name: TEXT
     - country: TEXT values=[US(400), UK(200), DE(150), ...]

CRITICAL RULES:
- ONE action per iteration — never output multiple Action/Action Input pairs
- TABLES line: comma-separated table names (use "all" or "none" if appropriate)
- CONTEXT section: Only include columns/info relevant to answering the question
- For columns used in WHERE filters, include value statistics (values=[...] or range=[...])
- For FK/JOIN columns, include the FK arrow notation (→ table.column)
- Keep it compact — the SQL generator will use this context directly

Output:`, claimedMaxIterations, schemaSection, query)

	// Execute ReAct — dump prompt to file for post-analysis
	if l.logger != nil {
		l.logger.FileOnly("\n┌─ Schema Linking ReAct Prompt ──────────────────────────────────────\n")
		l.logger.FileOnly("%s", prompt)
		l.logger.FileOnly("└──────────────────────────────────────────────────────────────────\n\n")
	}
	agentResult, err := executor.Call(ctx, map[string]any{"input": prompt})
	if err != nil {
		return nil, err
	}

	// Collect ReAct steps from handler
	collectedSteps := reactHandler.GetCollectedSteps()
	schemaLinkingSteps := make([]ReActStep, 0, len(collectedSteps))
	for _, step := range collectedSteps {
		schemaLinkingSteps = append(schemaLinkingSteps, ReActStep{
			Thought:     step.Thought,
			Action:      step.Action,
			ActionInput: step.ActionInput,
			Observation: step.Observation,
			Phase:       "schema_linking",
		})
	}

	// Extract final result — parse TABLES and CONTEXT sections
	if output, ok := agentResult["output"].(string); ok {
		tables, contextPrompt := parseSchemaLinkOutput(output)

		if len(tables) == 1 && tables[0] == "all" {
			allTableNames := make([]string, 0, len(allTables))
			for name := range allTables {
				allTableNames = append(allTableNames, name)
			}
			return &SchemaLinkResult{Tables: allTableNames, Steps: schemaLinkingSteps, ContextPrompt: contextPrompt}, nil
		}

		if len(tables) == 1 && tables[0] == "none" {
			return &SchemaLinkResult{Tables: []string{}, Steps: schemaLinkingSteps, ContextPrompt: contextPrompt}, nil
		}

		// Auto-complete FK-referenced tables
		tables = l.autoCompleteFKTables(tables, allTables)
		return &SchemaLinkResult{Tables: tables, Steps: schemaLinkingSteps, ContextPrompt: contextPrompt}, nil
	}

	return nil, fmt.Errorf("schema linking failed to produce a valid table list")
}

// parseSchemaLinkOutput parses the structured output from schema linking.
// Expected format:
//
//	TABLES: table1, table2
//	CONTEXT:
//	...focused schema text...
//
// Falls back to treating the entire output as comma-separated table names (legacy format).
func parseSchemaLinkOutput(output string) (tables []string, contextPrompt string) {
	output = strings.TrimSpace(output)

	// Try to find TABLES: line
	tablesIdx := strings.Index(output, "TABLES:")
	contextIdx := strings.Index(output, "CONTEXT:")

	if tablesIdx >= 0 {
		// Extract TABLES section
		tablesStart := tablesIdx + len("TABLES:")
		tablesEnd := len(output)
		if contextIdx > tablesStart {
			tablesEnd = contextIdx
		}
		tablesLine := strings.TrimSpace(output[tablesStart:tablesEnd])
		// Take only first line of tables section
		if nlIdx := strings.IndexByte(tablesLine, '\n'); nlIdx >= 0 {
			tablesLine = strings.TrimSpace(tablesLine[:nlIdx])
		}
		for _, t := range strings.Split(tablesLine, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tables = append(tables, t)
			}
		}

		// Extract CONTEXT section
		if contextIdx >= 0 {
			contextPrompt = strings.TrimSpace(output[contextIdx+len("CONTEXT:"):])
		}
		return
	}

	// Legacy fallback: first line is comma-separated table names
	lines := strings.Split(output, "\n")
	firstLine := strings.TrimSpace(lines[0])
	for _, t := range strings.Split(firstLine, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tables = append(tables, t)
		}
	}
	return
}

// autoCompleteFKTables adds FK-referenced tables that were selected tables depend on.
// For each selected table, if it has FK references to another table not in the set, add it.
// Also adds tables that reference selected tables (reverse FK — bridge tables).
func (l *LLMSchemaLinker) autoCompleteFKTables(selected []string, allTables map[string]*TableInfo) []string {
	selectedSet := make(map[string]bool, len(selected))
	for _, t := range selected {
		selectedSet[t] = true
	}

	// Forward FK: selected table references another table → add it
	for _, tableName := range selected {
		table, ok := allTables[tableName]
		if !ok {
			continue
		}
		for _, fk := range table.ForeignKeys {
			if !selectedSet[fk.ReferencedTable] {
				if _, exists := allTables[fk.ReferencedTable]; exists {
					selectedSet[fk.ReferencedTable] = true
					if l.logger != nil {
						l.logger.Printf("📋 Auto-added FK-referenced table: %s (referenced by %s.%s)\n",
							fk.ReferencedTable, tableName, fk.ColumnName)
					}
				}
			}
		}
	}

	// Reverse FK: if an unselected table references a selected table, and that
	// unselected table is also referenced by another selected table, add it (bridge table detection)
	for name, table := range allTables {
		if selectedSet[name] {
			continue
		}
		refsSelected := 0
		for _, fk := range table.ForeignKeys {
			if selectedSet[fk.ReferencedTable] {
				refsSelected++
			}
		}
		// If this unselected table references 2+ selected tables, it's likely a bridge table
		if refsSelected >= 2 {
			selectedSet[name] = true
			if l.logger != nil {
				l.logger.Printf("📋 Auto-added bridge table: %s (references %d selected tables)\n", name, refsSelected)
			}
		}
	}

	result := make([]string, 0, len(selectedSet))
	for t := range selectedSet {
		result = append(result, t)
	}
	return result
}

// ExtractTableInfo extracts table info from Rich Context
func ExtractTableInfo(ctx *contextpkg.SharedContext) map[string]*TableInfo {
	result := make(map[string]*TableInfo)

	for name, table := range ctx.Tables {
		columns := make([]string, len(table.Columns))
		for i, col := range table.Columns {
			columns[i] = col.Name
		}

		// Prefer LLM-generated description
		description := table.Description
		if description == "" {
			// Fallback: use table comment
			description = table.Comment
		}
		if description == "" && len(table.RichContext) > 0 {
			// Last resort: use first rich_context entry (skip metadata keys)
			for k, v := range table.RichContext {
				if !strings.HasSuffix(k, "_columns") && !strings.HasSuffix(k, "_indexes") &&
					!strings.HasSuffix(k, "_rowcount") && !strings.HasSuffix(k, "_foreignkeys") {
					description = v.Content
					break
				}
			}
		}

		// Build quality summary from structured issues
		qualitySummary := ""
		if len(table.QualityIssues) > 0 {
			var criticals []string
			for _, issue := range table.QualityIssues {
				if issue.Severity == "critical" {
					criticals = append(criticals, fmt.Sprintf("%s(%s)", issue.Column, issue.Type))
				}
			}
			if len(criticals) > 0 {
				qualitySummary = fmt.Sprintf("⚠️ Quality: %s", strings.Join(criticals, ", "))
			}
		}

		result[name] = &TableInfo{
			Name:           name,
			Columns:        columns,
			ForeignKeys:    table.ForeignKeys,
			Description:    description,
			QualitySummary: qualitySummary,
		}
	}

	return result
}
