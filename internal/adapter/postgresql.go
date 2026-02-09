package adapter

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// PostgreSQLAdapter PostgreSQL adapter
type PostgreSQLAdapter struct {
	db     *sql.DB
	config *PostgreSQLConfig
}

// PostgreSQLConfig PostgreSQL connection config
type PostgreSQLConfig struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
	SSLMode  string // disable, require, verify-ca, verify-full
}

// NewPostgreSQLAdapter creates PostgreSQL adapter
func NewPostgreSQLAdapter(config *PostgreSQLConfig) *PostgreSQLAdapter {
	if config.SSLMode == "" {
		config.SSLMode = "disable"
	}
	return &PostgreSQLAdapter{
		config: config,
	}
}

// Connect connects to database
func (a *PostgreSQLAdapter) Connect(ctx context.Context) error {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		a.config.Host,
		a.config.Port,
		a.config.User,
		a.config.Password,
		a.config.Database,
		a.config.SSLMode,
	)

	db, err := sql.Open("postgres", dsn)
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
func (a *PostgreSQLAdapter) Close() error {
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

// ExecuteQuery executes query
func (a *PostgreSQLAdapter) ExecuteQuery(ctx context.Context, query string) (*QueryResult, error) {
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
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		row := make(map[string]interface{})
		for i, col := range columns {
			val := values[i]
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
func (a *PostgreSQLAdapter) GetDatabaseType() string {
	return "PostgreSQL"
}

// GetDatabaseVersion gets database version
func (a *PostgreSQLAdapter) GetDatabaseVersion(ctx context.Context) (string, error) {
	result, err := a.ExecuteQuery(ctx, "SELECT version() as version")
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
