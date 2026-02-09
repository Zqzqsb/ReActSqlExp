package adapter

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// MySQLAdapter MySQL adapter
type MySQLAdapter struct {
	db     *sql.DB
	config *MySQLConfig
}

// MySQLConfig MySQL connection config
type MySQLConfig struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
}

// NewMySQLAdapter creates MySQL adapter
func NewMySQLAdapter(config *MySQLConfig) *MySQLAdapter {
	return &MySQLAdapter{
		config: config,
	}
}

// Connect connects to database
func (a *MySQLAdapter) Connect(ctx context.Context) error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true",
		a.config.User,
		a.config.Password,
		a.config.Host,
		a.config.Port,
		a.config.Database,
	)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	a.db = db
	return nil
}

// Close closes connection
func (a *MySQLAdapter) Close() error {
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

// ExecuteQuery executes query
func (a *MySQLAdapter) ExecuteQuery(ctx context.Context, query string) (*QueryResult, error) {
	start := time.Now()

	rows, err := a.db.QueryContext(ctx, query)
	if err != nil {
		return &QueryResult{
			Error:         err.Error(),
			ExecutionTime: time.Since(start).Milliseconds(),
		}, err // Return error for caller to handle
	}
	defer rows.Close()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	// Read data
	var result []map[string]interface{}
	for rows.Next() {
		// Create scan targets
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		// Scan row
		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		// Build map
		row := make(map[string]interface{})
		for i, col := range columns {
			val := values[i]
			// Handle []byte type
			if b, ok := val.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = val
			}
		}
		result = append(result, row)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &QueryResult{
		Columns:       columns,
		Rows:          result,
		RowCount:      len(result),
		ExecutionTime: time.Since(start).Milliseconds(),
	}, nil
}

// GetDatabaseType gets database type
func (a *MySQLAdapter) GetDatabaseType() string {
	return "MySQL"
}

// GetDatabaseVersion gets database version
func (a *MySQLAdapter) GetDatabaseVersion(ctx context.Context) (string, error) {
	result, err := a.ExecuteQuery(ctx, "SELECT VERSION() as version")
	if err != nil {
		return "", err
	}
	if result.Error != "" {
		return "", fmt.Errorf(result.Error)
	}
	if len(result.Rows) > 0 {
		if version, ok := result.Rows[0]["version"].(string); ok {
			return version, nil
		}
	}
	return "unknown", nil
}
