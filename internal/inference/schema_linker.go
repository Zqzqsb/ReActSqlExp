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

// SchemaLinker module interface
type SchemaLinker interface {
	// Link performs Schema Linking
	// Input: query, all table info
	// Output: relevant table names, ReAct steps (if using ReAct mode)
	Link(ctx context.Context, query string, allTables map[string]*TableInfo) ([]string, []ReActStep, error)
}

// TableInfo brief table info (for Schema Linking)
type TableInfo struct {
	Name        string
	Columns     []string                        // Column name list
	ForeignKeys []contextpkg.ForeignKeyMetadata // Foreign key relationships
	Description string                          // Table description (optional, from rich_context or table comment)
}

// LLMSchemaLinker LLM-based Schema Linking
type LLMSchemaLinker struct {
	llm           llms.Model
	adapter       adapter.DBAdapter
	useReact      bool
	tokenRecorder func(prompt, response string)
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
func (l *LLMSchemaLinker) Link(ctx context.Context, query string, allTables map[string]*TableInfo) ([]string, []ReActStep, error) {
	if l.useReact {
		return l.linkWithReact(ctx, query, allTables)
	}
	return l.linkOneShot(ctx, query, allTables)
}

// linkOneShot One-shot Schema Linking
func (l *LLMSchemaLinker) linkOneShot(ctx context.Context, query string, allTables map[string]*TableInfo) ([]string, []ReActStep, error) {
	// Build table info description (formatted as readable list)
	var schemaDesc strings.Builder
	for _, table := range allTables {
		schemaDesc.WriteString(fmt.Sprintf("- %s\n", table.Name))
		schemaDesc.WriteString(fmt.Sprintf("  Columns: %s\n", strings.Join(table.Columns, ", ")))
		if table.Description != "" {
			schemaDesc.WriteString(fmt.Sprintf("  Description: %s\n", table.Description))
		}
		schemaDesc.WriteString("\n")
	}

	// Build Prompt
	prompt := fmt.Sprintf(`You are a database expert. Identify which tables are relevant to answer the question.

Available Tables:
%s

Question: %s

Task: Select the minimum set of tables needed to answer this question.
Output format: table1, table2, table3 (comma-separated, no extra text)
If all tables are needed, output: all
If no tables are needed, output: none

Output:`, schemaDesc.String(), query)

	// Skip full Schema Linking prompt print
	fmt.Println("🔍 Schema Linking...")

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
			fmt.Printf("⚠️  Schema Linking failed (attempt %d/%d): %v\n", attempt+1, maxRetries+1, err)
			fmt.Printf("⏳ Retrying after %v...\n\n", delay)
			time.Sleep(delay)
		}
	}

	if err != nil {
		return nil, []ReActStep{}, fmt.Errorf("schema linking failed after %d attempts: %w", maxRetries+1, err)
	}

	response = strings.TrimSpace(response)

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
		// Create a simple step to represent Schema Linking process
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
		return result, steps, nil
	}

	if response == "none" {
		// Create a simple step to represent Schema Linking process
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
		return []string{}, steps, nil
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

	return result, steps, nil
}

// linkWithReact ReAct mode Schema Linking
func (l *LLMSchemaLinker) linkWithReact(ctx context.Context, query string, allTables map[string]*TableInfo) ([]string, []ReActStep, error) {
	fmt.Println("🔍 Schema Linking (ReAct mode)...")

	// Create SQL tool
	sqlTool := &SQLTool{
		adapter:   l.adapter,
		useDryRun: false,
	}

	// Create handler to collect ReAct steps
	reactHandler := &PrettyReActHandler{logMode: "simple"}

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
		return nil, []ReActStep{}, err
	}

	// Build table info
	var schemaDesc strings.Builder
	for _, table := range allTables {
		schemaDesc.WriteString(fmt.Sprintf("- %s\n", table.Name))
		schemaDesc.WriteString(fmt.Sprintf("  Columns: %s\n", strings.Join(table.Columns, ", ")))

		// Add FK info
		if len(table.ForeignKeys) > 0 {
			schemaDesc.WriteString("  Foreign Keys:\n")
			for _, fk := range table.ForeignKeys {
				schemaDesc.WriteString(fmt.Sprintf("    %s → %s.%s\n", fk.ColumnName, fk.ReferencedTable, fk.ReferencedColumn))
			}
		}

		if table.Description != "" {
			schemaDesc.WriteString(fmt.Sprintf("  Description: %s\n", table.Description))
		}
		schemaDesc.WriteString("\n")
	}

	// Build Prompt
	prompt := fmt.Sprintf(`You are a database expert. Identify which tables are relevant to answer the question.

⚠️  ITERATION LIMIT: You have maximum %d iterations to complete this task. Be efficient!

Available Tables:
%s

Question: %s

Foreign key relationships are shown above. Use them to:
1. Identify direct relationships between tables
2. Find intermediate junction tables for many-to-many relationships
3. Trace the join path from source to target tables

You can use execute_sql to:
- Verify data existence: SELECT COUNT(*) FROM table
- Check join validity: SELECT COUNT(*) FROM t1 JOIN t2 ON ...
- Explore sample data: SELECT * FROM table LIMIT 3
- Check column values: SELECT DISTINCT column FROM table LIMIT 5

Workflow:
1. Identify tables with columns that seem relevant to the question.
2. Use the foreign key relationships to find all necessary tables for joins.
3. If you are unsure about a table's relevance, use 'execute_sql' to sample its data.
4. Provide the final list of tables.

Output Format:
A) Use tool to explore:
   Thought: [reasoning]
   Action: execute_sql
   Action Input: [SQL query]

B) Give final answer:
   Thought: [reasoning]
   Final Answer: table1, table2, table3

IMPORTANT:
- Output comma-separated table names only in Final Answer
- Include ALL tables needed for joins (don't miss intermediate tables)
- For NOT queries, include base table
- For foreign key columns, include referenced tables
- If all tables needed, output: all
- If no tables needed, output: none

Output:`, claimedMaxIterations, schemaDesc.String(), query)

	// Execute ReAct
	agentResult, err := executor.Call(ctx, map[string]any{"input": prompt})
	if err != nil {
		return nil, []ReActStep{}, err
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

	// Extract final result
	if output, ok := agentResult["output"].(string); ok {
		// Take first line only (LLM may include extra explanation)
		lines := strings.Split(output, "\n")
		firstLine := strings.TrimSpace(lines[0])

		if firstLine == "all" {
			result := make([]string, 0, len(allTables))
			for name := range allTables {
				result = append(result, name)
			}
			return result, schemaLinkingSteps, nil
		}

		if firstLine == "none" {
			return []string{}, schemaLinkingSteps, nil
		}

		tables := strings.Split(firstLine, ",")
		result := make([]string, 0, len(tables))
		for _, table := range tables {
			table = strings.TrimSpace(table)
			if table != "" {
				result = append(result, table)
			}
		}
		return result, schemaLinkingSteps, nil
	}

	return nil, []ReActStep{}, fmt.Errorf("schema linking failed to produce a valid table list")
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
			// Last resort: use first rich_context entry
			for _, v := range table.RichContext {
				description = v.Content
				break
			}
		}

		result[name] = &TableInfo{
			Name:        name,
			Columns:     columns,
			ForeignKeys: table.ForeignKeys,
			Description: description,
		}
	}

	return result
}
