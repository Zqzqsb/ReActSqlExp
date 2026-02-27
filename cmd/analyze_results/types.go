package main

import (
	"strings"
)

// DBType represents supported database type
type DBType int

// Database type enum
const (
	DBTypeUnknown DBType = iota
	DBTypeSQLite
	DBTypePostgreSQL
	DBTypeMySQL
)

// String returns the string representation of DBType
func (t DBType) String() string {
	switch t {
	case DBTypeSQLite:
		return "sqlite"
	case DBTypePostgreSQL:
		return "postgresql"
	case DBTypeMySQL:
		return "mysql"
	default:
		return "unknown"
	}
}

// ParseDBType converts string to DBType
func ParseDBType(dbType string) DBType {
	switch strings.ToLower(dbType) {
	case "sqlite":
		return DBTypeSQLite
	case "postgresql", "postgres":
		return DBTypePostgreSQL
	case "mysql":
		return DBTypeMySQL
	default:
		return DBTypeUnknown
	}
}

// InputResult represents input SQL result structure
type InputResult struct {
	ID         int    `json:"id"`
	DBName     string `json:"db_name"`
	Question   string `json:"question"`
	GTSQL      string `json:"gt_sql"`
	PredSQL    string `json:"pred_sql"`
	Thinking   string `json:"thinking,omitempty"`
	Ambiguous  string `json:"ambiguous,omitempty"`
	SPJType    string `json:"spj_type,omitempty"`    // SPJ type tag
	Difficulty string `json:"difficulty,omitempty"` // simple/moderate/challenging
}

// AnalysisResult represents analyzed SQL result structure
type AnalysisResult struct {
	ID           int    `json:"id"`
	DBName       string `json:"db_id"`
	Question     string `json:"question"`
	GTSQL        string `json:"gt_sql"`
	PredSQL      string `json:"pred_sql"`
	IsCorrect    bool   `json:"is_correct"`
	IsEquivalent bool   `json:"is_equivalent"`
	ErrorReason  string `json:"error_reason,omitempty"`
	ErrorType    string `json:"error_type,omitempty"`
	Thinking     string `json:"thinking[optional],omitempty"`
	Ambiguous    string `json:"ambigous[optional],omitempty"`
	Difficulty   string `json:"difficulty,omitempty"`
	SPJType      string `json:"spj_type,omitempty"`   // SPJ type
	SPJResult    string `json:"spj_result,omitempty"` // SPJ judgment description

	// Execution result
	GTResult   *ExecResult `json:"gt_result,omitempty"`
	PredResult *ExecResult `json:"pred_result,omitempty"`
}

// ExecResult represents SQL execution result
type ExecResult struct {
	Success bool       `json:"Success"`
	Error   string     `json:"Error"`
	Rows    [][]string `json:"Rows"`
}

// ErrorCount for error statistics sorting
type ErrorCount struct {
	Reason string
	Count  int
	Type   string
}

// ErrorStatistics stores error statistics
type ErrorStatistics struct {
	TotalCount           int
	CorrectCount         int
	AmbiguousCount       int
	EquivalentCount      int
	DBNotExistCount      int
	SyntaxErrorCount     int
	ProjectionErrorCount int
	DataErrorCount       int
	RowErrorCount        int // Dedicated row count error counter
	ReferenceErrorCount  int // Reference answer syntax error
	ExecutionErrorCount  int // execution error (pred SQL syntax error)
	// Below fields deprecated, kept for backward compat
	OrderErrorCount     int
	JoinErrorCount      int
	ConditionErrorCount int
	OtherErrorCount     int
	ErrorCounts         []ErrorCount

	TimeoutCount int // queries that timed out during execution

	// SPJ statistics
	SPJCaseCount      int // Total SPJ cases
	SPJCorrectCount   int // SPJ correct count
	SPJIncorrectCount int // SPJ incorrect count
}
