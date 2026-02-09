package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pkoukk/tiktoken-go"
	"github.com/tmc/langchaingo/llms"

	"reactsql/internal/adapter"
	contextpkg "reactsql/internal/context"
)

// Config inference pipeline configuration
type Config struct {
	UseRichContext bool
	UseReact       bool
	ReactLinking   bool // Whether Schema Linking uses ReAct mode
	UseDryRun      bool
	MaxIterations  int
	ContextFile    string

	// Clarify feature config
	ClarifyMode             string   // Clarify mode: "off" (off) | "on" (agent asks) | "force" (forced)
	LogMode                 string   // Log mode: "simple" (simple) | "full" (full)
	ResultFields            []string // Expected result field list
	ResultFieldsDescription string   // Result field descriptions

	// Proofread config
	EnableProofread bool   // Enable proofread (allow LLM to fix Rich Context)
	DBName          string // Database name
	DBType          string // Database type
}

// StepCallback is called for each ReAct step update during streaming
// eventType: "thought" | "action" | "observation" | "finish"
type StepCallback func(step ReActStep, eventType string)

// Pipeline inference pipeline
type Pipeline struct {
	llm          llms.Model
	adapter      adapter.DBAdapter
	config       *Config
	context      *contextpkg.SharedContext
	schemaLinker SchemaLinker
	tokenizer    *tiktoken.Tiktoken

	// Token statistics accumulator
	promptTexts   []string
	responseTexts []string

	// Streaming callback
	stepCallback StepCallback
}

// Result inference result
type Result struct {
	Query           string
	GeneratedSQL    string
	ExecutionResult interface{}

	// Statistics
	TotalTime     time.Duration
	LLMCalls      int
	SQLExecutions int
	TotalTokens   int
	ClarifyCount  int // Clarify count

	// Intermediate results
	SelectedTables []string
	ReActSteps     []ReActStep
}

// ReActStep represents a ReAct step
type ReActStep struct {
	Step        int         `json:"step,omitempty"`              // Step number for streaming
	Thought     string      `json:"thought"`
	Action      string      `json:"action"`
	ActionInput interface{} `json:"action_input,omitempty"` // Supports string and map[string]interface{}
	Observation string      `json:"observation,omitempty"`
	Phase       string      `json:"phase,omitempty"` // "schema_linking" or "sql_generation"
}

// Reset cleans accumulated stats to prevent memory leaks
func (p *Pipeline) Reset() {
	p.promptTexts = nil
	p.responseTexts = nil
	p.stepCallback = nil
}

// SetStepCallback sets the callback function for streaming ReAct steps
func (p *Pipeline) SetStepCallback(callback StepCallback) {
	p.stepCallback = callback
}

// notifyStep notifies the callback of a ReAct step update
func (p *Pipeline) notifyStep(step ReActStep, eventType string) {
	if p.stepCallback != nil {
		p.stepCallback(step, eventType)
	}
}

// NewPipeline creates inference pipeline
func NewPipeline(llm llms.Model, adapter adapter.DBAdapter, config *Config) *Pipeline {
	// Initialize tokenizer (using cl100k_base for GPT-3.5/GPT-4/DeepSeek)
	tokenizer, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		// If failed, use nil, skip token counting later
		tokenizer = nil
	}

	// Schema Linking uses ReAct mode (controlled by ReactLinking config)
	linker := NewLLMSchemaLinker(llm, adapter, config.ReactLinking)

	p := &Pipeline{
		llm:          llm,
		adapter:      adapter,
		config:       config,
		schemaLinker: linker,
		tokenizer:    tokenizer,
	}

	// Set token recorder
	linker.tokenRecorder = func(prompt, response string) {
		p.promptTexts = append(p.promptTexts, prompt)
		p.responseTexts = append(p.responseTexts, response)
	}

	// Load Context file (if provided)
	// Note: context always loaded for Schema Linking
	// UseRichContext only controls using rich_context in SQL Generation
	if config.ContextFile != "" {
		if ctx, err := p.loadContext(config.ContextFile); err == nil {
			p.context = ctx
		}
	}

	return p
}

// countTokens counts text token count
func (p *Pipeline) countTokens(text string) int {
	if p.tokenizer == nil {
		return 0
	}
	tokens := p.tokenizer.Encode(text, nil, nil)
	return len(tokens)
}

// Execute runs inference
func (p *Pipeline) Execute(ctx context.Context, query string) (*Result, error) {
	startTime := time.Now()

	// Reset token stat accumulator
	p.promptTexts = []string{}
	p.responseTexts = []string{}

	result := &Result{
		Query:      query,
		ReActSteps: []ReActStep{},
	}

	// 1. Schema Linking (always runs, identifies relevant tables)
	var allTableInfo map[string]*TableInfo
	var err error
	if p.context != nil {
		// Extract table info from Rich Context
		allTableInfo = ExtractTableInfo(p.context)
	} else {
		// Query table info from DB
		allTableInfo, err = p.extractTableInfoFromDB(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to extract table info: %w", err)
		}
	}

	tables, schemaLinkingSteps, err := p.schemaLinker.Link(ctx, query, allTableInfo)
	if err != nil {
		return nil, fmt.Errorf("schema linking failed: %w", err)
	}
	result.SelectedTables = tables
	result.LLMCalls++

	// Add Schema Linking ReAct steps to result
	for _, step := range schemaLinkingSteps {
		result.ReActSteps = append(result.ReActSteps, ReActStep{
			Thought:     step.Thought,
			Action:      step.Action,
			ActionInput: step.ActionInput,
			Observation: step.Observation,
			Phase:       "schema_linking",
		})
	}

	fmt.Printf("📋 Selected Tables: %v\n\n", tables)

	// 2. Build Schema Context (basic table structure, always provided)
	var contextPrompt string

	if p.config.UseRichContext && p.context != nil {
		// Use Rich Context (detailed info)
		opts := &contextpkg.ExportOptions{
			Tables:             tables,
			IncludeColumns:     true,
			IncludeIndexes:     true,
			IncludeRichContext: true,
			IncludeStats:       true,
		}
		contextPrompt = p.context.ExportToCompactPrompt(opts)
		// Print summary only, not full Rich Context
		fmt.Printf("📚 Using Rich Context for %d tables\n", len(tables))
	} else {
		// Use basic Schema (table+column names only)
		contextPrompt = p.buildBasicSchema(ctx, tables)
		// Skip full Basic Schema print
		fmt.Printf("📋 Using Basic Schema for %d tables\n", len(tables))
	}

	// 3. Generate SQL
	var sql string
	if p.config.UseReact {
		sql, err = p.reactLoop(ctx, query, contextPrompt, result)
	} else {
		sql, err = p.oneShotGeneration(ctx, query, contextPrompt)
		result.LLMCalls++
	}

	if err != nil {
		return nil, fmt.Errorf("SQL generation failed: %w", err)
	}

	result.GeneratedSQL = sql
	result.TotalTime = time.Since(startTime)

	// 4. Count tokens (from all accumulated prompts and responses)
	// Token counting temporarily disabled to avoid potential issues
	fmt.Printf("[DEBUG] Token counting disabled (would count %d prompts, %d responses)\n", len(p.promptTexts), len(p.responseTexts))
	result.TotalTokens = 0 // temporarily set to 0

	// if p.tokenizer != nil {
	// 	for i, prompt := range p.promptTexts {
	// 		fmt.Printf("[DEBUG] Counting prompt %d/%d (length: %d)\n", i+1, len(p.promptTexts), len(prompt))
	// 		result.TotalTokens += p.countTokens(prompt)
	// 	}
	// 	for i, response := range p.responseTexts {
	// 		fmt.Printf("[DEBUG] Counting response %d/%d (length: %d)\n", i+1, len(p.responseTexts), len(response))
	// 		result.TotalTokens += p.countTokens(response)
	// 	}
	// }

	// 5. Execute SQL (optional)
	if sql != "" {
		execResult, err := p.adapter.ExecuteQuery(ctx, sql)
		if err == nil {
			result.ExecutionResult = execResult
			result.SQLExecutions++
		}
	}

	return result, nil
}

// loadContext loads Rich Context
func (p *Pipeline) loadContext(path string) (*contextpkg.SharedContext, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var ctx contextpkg.SharedContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		return nil, err
	}

	return &ctx, nil
}

// extractTableInfoFromDB extracts table info from DB
func (p *Pipeline) extractTableInfoFromDB(ctx context.Context) (map[string]*TableInfo, error) {
	// Get all table names
	var query string
	switch p.adapter.GetDatabaseType() {
	case "MySQL":
		query = "SHOW TABLES"
	case "PostgreSQL":
		query = "SELECT tablename FROM pg_tables WHERE schemaname='public'"
	case "SQLite":
		query = "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"
	default:
		return nil, fmt.Errorf("unsupported database type")
	}

	result, err := p.adapter.ExecuteQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	tableInfo := make(map[string]*TableInfo)

	// Query column info for each table
	for _, row := range result.Rows {
		var tableName string
		for _, val := range row {
			if name, ok := val.(string); ok {
				tableName = name
				break
			}
		}

		if tableName == "" {
			continue
		}

		// Query column info
		var colQuery string
		switch p.adapter.GetDatabaseType() {
		case "MySQL":
			colQuery = fmt.Sprintf("DESCRIBE %s", tableName)
		case "SQLite":
			colQuery = fmt.Sprintf("PRAGMA table_info(%s)", tableName)
		case "PostgreSQL":
			colQuery = fmt.Sprintf("SELECT column_name FROM information_schema.columns WHERE table_name='%s'", tableName)
		}

		colResult, err := p.adapter.ExecuteQuery(ctx, colQuery)
		if err != nil {
			continue
		}

		columns := make([]string, 0, len(colResult.Rows))
		for _, colRow := range colResult.Rows {
			var colName string
			switch p.adapter.GetDatabaseType() {
			case "MySQL":
				if field, ok := colRow["Field"].(string); ok {
					colName = field
				}
			case "SQLite":
				if name, ok := colRow["name"].(string); ok {
					colName = name
				}
			case "PostgreSQL":
				if name, ok := colRow["column_name"].(string); ok {
					colName = name
				}
			}

			if colName != "" {
				columns = append(columns, colName)
			}
		}

		tableInfo[tableName] = &TableInfo{
			Name:    tableName,
			Columns: columns,
		}
	}

	return tableInfo, nil
}

// buildBasicSchema builds basic schema from DB table structure
func (p *Pipeline) buildBasicSchema(ctx context.Context, tables []string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Database: %s\n\n", p.adapter.GetDatabaseType()))

	for _, tableName := range tables {
		// Query table structure
		var query string
		switch p.adapter.GetDatabaseType() {
		case "MySQL":
			query = fmt.Sprintf("DESCRIBE %s", tableName)
		case "SQLite":
			query = fmt.Sprintf("PRAGMA table_info(%s)", tableName)
		case "PostgreSQL":
			query = fmt.Sprintf("SELECT column_name, data_type FROM information_schema.columns WHERE table_name='%s'", tableName)
		default:
			continue
		}

		result, err := p.adapter.ExecuteQuery(ctx, query)
		if err != nil {
			continue
		}

		// Format table structure
		sb.WriteString(fmt.Sprintf("Table %s:\n", tableName))

		for _, row := range result.Rows {
			var colName, colType string

			// Extract column name and type based on DB type
			switch p.adapter.GetDatabaseType() {
			case "MySQL":
				if field, ok := row["Field"].(string); ok {
					colName = field
				}
				if typ, ok := row["Type"].(string); ok {
					colType = typ
				}
			case "SQLite":
				if name, ok := row["name"].(string); ok {
					colName = name
				}
				if typ, ok := row["type"].(string); ok {
					colType = typ
				}
			case "PostgreSQL":
				if name, ok := row["column_name"].(string); ok {
					colName = name
				}
				if typ, ok := row["data_type"].(string); ok {
					colType = typ
				}
			}

			if colName != "" {
				sb.WriteString(fmt.Sprintf("  - %s: %s\n", colName, colType))
			}
		}

		sb.WriteString("\n")
	}

	return sb.String()
}
