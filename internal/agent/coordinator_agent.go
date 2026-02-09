package agent

import (
	"context"
	"fmt"

	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/tools"

	"reactsql/internal/adapter"
	contextpkg "reactsql/internal/context"
)

// CoordinatorAgent 调度Agent
type CoordinatorAgent struct {
	id        string
	llm       llms.Model
	adapter   adapter.DBAdapter
	sharedCtx *contextpkg.SharedContext
	executor  *agents.Executor
}

// NewCoordinatorAgent 创建调度Agent
func NewCoordinatorAgent(
	id string,
	llm llms.Model,
	adapter adapter.DBAdapter,
	sharedCtx *contextpkg.SharedContext,
) (*CoordinatorAgent, error) {

	agent := &CoordinatorAgent{
		id:        id,
		llm:       llm,
		adapter:   adapter,
		sharedCtx: sharedCtx,
	}

	// 创建工具
	sqlTool := &CoordinatorSQLTool{
		adapter:   adapter,
		sharedCtx: sharedCtx,
		agentID:   id,
	}

	// 创建LangChain executor
	executor, err := agents.Initialize(
		llm,
		[]tools.Tool{sqlTool},
		agents.ZeroShotReactDescription,
		agents.WithMaxIterations(15),
	)
	if err != nil {
		return nil, err
	}

	agent.executor = executor
	return agent, nil
}

// Execute 执行协调任务
func (a *CoordinatorAgent) Execute(ctx context.Context) error {
	if !a.sharedCtx.Quiet {
		fmt.Printf("\n[%s] Starting coordination...\n", a.id)
	}

	// 根据数据库类型选择查询语句
	var discoverQuery string
	switch a.adapter.GetDatabaseType() {
	case "MySQL":
		discoverQuery = "SHOW TABLES"
	case "PostgreSQL":
		discoverQuery = "SELECT tablename FROM pg_tables WHERE schemaname='public'"
	case "SQLite":
		discoverQuery = "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'"
	default:
		discoverQuery = "SHOW TABLES"
	}

	prompt := fmt.Sprintf(`You are a Coordinator Agent for database analysis.

Your mission: Analyze database "%s" (%s) and discover ALL tables, then register tasks for workers.

Your workflow:
1. Execute: %s - to discover all tables
2. For EACH table found, register a task in the shared context
3. Report completion when all tasks are registered

IMPORTANT: 
- Use execute_sql tool to query the database
- After discovering tables, your job is DONE
- Worker agents will handle the detailed analysis

Current context:
%s

Start by discovering tables.`, a.sharedCtx.DatabaseName, a.adapter.GetDatabaseType(), discoverQuery, a.sharedCtx.GetSummary())

	result, err := a.executor.Call(ctx, map[string]any{"input": prompt})
	if err != nil {
		return fmt.Errorf("coordinator failed: %w", err)
	}

	if !a.sharedCtx.Quiet {
		fmt.Printf("\n[%s] Coordination complete: %v\n", a.id, result)
	}
	return nil
}

// CoordinatorSQLTool SQL工具（用于协调Agent）
type CoordinatorSQLTool struct {
	adapter   adapter.DBAdapter
	sharedCtx *contextpkg.SharedContext
	agentID   string
}

func (t *CoordinatorSQLTool) Name() string {
	return "execute_sql"
}

func (t *CoordinatorSQLTool) Description() string {
	return `Execute SQL queries to discover database structure.

Use this to:
- Discover tables: SHOW TABLES
- Get database info: SELECT VERSION(), SELECT DATABASE()

After discovering tables, register tasks for each table in the shared context.`
}

func (t *CoordinatorSQLTool) Call(ctx context.Context, input string) (string, error) {
	if !t.sharedCtx.Quiet {
		fmt.Printf("\n[%s] SQL: %s\n", t.agentID, input)
	}

	// 执行SQL
	result, err := t.adapter.ExecuteQuery(ctx, input)
	if err != nil {
		return "", err
	}

	if result.Error != "" {
		return fmt.Sprintf("SQL Error: %s", result.Error), nil
	}

	// 格式化结果
	output := fmt.Sprintf("Query successful! (%d rows, %dms)\n\n", result.RowCount, result.ExecutionTime)

	// 显示结果
	if result.RowCount > 0 {
		output += "Results:\n"
		for i, row := range result.Rows {
			if i >= 10 {
				output += fmt.Sprintf("... and %d more rows\n", result.RowCount-10)
				break
			}
			output += fmt.Sprintf("  %v\n", row)
		}
	}

	// 如果是发现表的查询，自动注册任务
	isDiscoverQuery := contains(input, "SHOW TABLES") ||
		contains(input, "sqlite_master") ||
		contains(input, "pg_tables")

	if isDiscoverQuery && result.RowCount > 0 {
		output += "\n📋 Auto-registering tasks for discovered tables:\n"
		for _, row := range result.Rows {
			for _, val := range row {
				if tableName, ok := val.(string); ok {
					taskID := "analyze_" + tableName
					err := t.sharedCtx.RegisterTask(
						taskID,
						"worker_"+tableName,
						fmt.Sprintf("Analyze table: %s", tableName),
					)
					if err == nil {
						output += fmt.Sprintf("  ✓ Registered: %s\n", taskID)
					}
				}
			}
		}
	}

	output += "\n" + t.sharedCtx.GetSummary()

	return output, nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
			findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
