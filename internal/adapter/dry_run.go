package adapter

import (
	"context"
	"fmt"
)

// DryRunSQL validates SQL syntax (dry run)
func (a *MySQLAdapter) DryRunSQL(ctx context.Context, sql string) error {
	// MySQL: Use EXPLAIN to validate syntax
	explainSQL := fmt.Sprintf("EXPLAIN %s", sql)
	_, err := a.ExecuteQuery(ctx, explainSQL)
	return err
}

// DryRunSQL SQLite Dry Run
func (a *SQLiteAdapter) DryRunSQL(ctx context.Context, sql string) error {
	// SQLite: Use EXPLAIN QUERY PLAN
	explainSQL := fmt.Sprintf("EXPLAIN QUERY PLAN %s", sql)
	_, err := a.ExecuteQuery(ctx, explainSQL)
	return err
}

// DryRunSQL PostgreSQL Dry Run
func (a *PostgreSQLAdapter) DryRunSQL(ctx context.Context, sql string) error {
	// PostgreSQL: Use EXPLAIN
	explainSQL := fmt.Sprintf("EXPLAIN %s", sql)
	_, err := a.ExecuteQuery(ctx, explainSQL)
	return err
}

// DBAdapter interface DryRunSQL method
// Method declared in adapter.go
