package adapter

import (
	"context"
)

// DatabaseType database type enum
type DatabaseType string

const (
	MySQL      DatabaseType = "mysql"
	PostgreSQL DatabaseType = "postgresql"
	SQLite     DatabaseType = "sqlite"
)

// DBAdapter database adapter interface
// Lightweight: only handles connection and SQL execution
type DBAdapter interface {
	// Connect connects to database
	Connect(ctx context.Context) error

	// Close closes connection
	Close() error

	// ExecuteQuery executes query
	// Returns unified QueryResult with columns, rows, execution time
	ExecuteQuery(ctx context.Context, query string) (*QueryResult, error)

	// GetDatabaseType gets database type
	// Returns: "MySQL", "PostgreSQL", "SQLite" etc.
	GetDatabaseType() string

	// GetDatabaseVersion gets database version (optional)
	GetDatabaseVersion(ctx context.Context) (string, error)

	// DryRunSQL validates SQL syntax (dry run)
	DryRunSQL(ctx context.Context, sql string) error
}

// QueryResult query result (unified structure)
type QueryResult struct {
	Columns       []string                 // Column name
	Rows          []map[string]interface{} // Data rows (unified map format)
	RowCount      int                      // Row count
	ExecutionTime int64                    // Execution time (ms)
	Error         string                   // Error message (if any)
}

// DBConfig database connection config (generic)
type DBConfig struct {
	Type     string // Database type: "mysql", "postgresql", "sqlite"
	Host     string // Host address
	Port     int    // Port
	Database string // Database name
	User     string // Username
	Password string // Password

	// SQLite specific
	FilePath string // SQLite file path

	// Connection pool config (optional)
	MaxOpenConns int // Max open connections
	MaxIdleConns int // Max idle connections
}

// NewAdapter factory: creates adapter based on config
func NewAdapter(config *DBConfig) (DBAdapter, error) {
	switch config.Type {
	case "mysql":
		return NewMySQLAdapter(&MySQLConfig{
			Host:     config.Host,
			Port:     config.Port,
			Database: config.Database,
			User:     config.User,
			Password: config.Password,
		}), nil
	case "postgresql":
		return NewPostgreSQLAdapter(&PostgreSQLConfig{
			Host:     config.Host,
			Port:     config.Port,
			Database: config.Database,
			User:     config.User,
			Password: config.Password,
		}), nil
	case "sqlite":
		return NewSQLiteAdapter(&SQLiteConfig{
			FilePath: config.FilePath,
		}), nil
	default:
		return nil, &UnsupportedDatabaseError{Type: config.Type}
	}
}

// UnsupportedDatabaseError unsupported database type error
type UnsupportedDatabaseError struct {
	Type string
}

func (e *UnsupportedDatabaseError) Error() string {
	return "unsupported database type: " + e.Type
}
