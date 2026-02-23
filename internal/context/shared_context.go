package context

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// TaskStatus task status enum
type TaskStatus int

const (
	TaskRegistered TaskStatus = iota // registered
	TaskRunning                      // running
	TaskCompleted                    // completed
	TaskFailed                       // failed
)

func (s TaskStatus) String() string {
	switch s {
	case TaskRegistered:
		return "REGISTERED"
	case TaskRunning:
		return "RUNNING"
	case TaskCompleted:
		return "COMPLETED"
	case TaskFailed:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

// TaskInfo task information
type TaskInfo struct {
	ID          string                 `json:"id"`
	AgentID     string                 `json:"agent_id"`
	Description string                 `json:"description"`
	Status      TaskStatus             `json:"status"`
	StartTime   time.Time              `json:"start_time"`
	EndTime     time.Time              `json:"end_time,omitempty"`
	Result      map[string]interface{} `json:"result,omitempty"`
	Error       string                 `json:"error,omitempty"`
}

// SchemaDiagram database relationship diagram
type SchemaDiagram struct {
	Format      string `json:"format"`      // "mermaid-er"
	Description string `json:"description"` // Diagram description
	Content     string `json:"content"`     // Mermaid code
}

// SharedContext shared context (multi-agent collaboration)
type SharedContext struct {
	// Database info
	DatabaseName string    `json:"database_name"`
	DatabaseType string    `json:"database_type"`
	Version      string    `json:"version,omitempty"`
	CollectedAt  time.Time `json:"collected_at"`

	// Schema diagram
	SchemaDiagram *SchemaDiagram `json:"schema_diagram,omitempty"`

	// Metadata (clean DB metadata)
	Tables      map[string]*TableMetadata `json:"tables"`
	TotalTables int                       `json:"total_tables"`
	TotalRows   int64                     `json:"total_rows"`

	// JOIN path analysis
	JoinPaths map[string]*JoinPath `json:"join_paths,omitempty"`

	// Field semantic info
	FieldSemantics map[string]*FieldSemantic `json:"field_semantics,omitempty"`

	// Task registry (not saved to JSON)
	tasks map[string]*TaskInfo `json:"-"`

	// Temp data (not saved to JSON)
	tempData map[string]interface{} `json:"-"`

	// Concurrency control
	mu sync.RWMutex `json:"-"`

	// Quiet suppresses verbose log output (used by gen_all_dev multi-progress mode)
	Quiet bool `json:"-"`
}

// BusinessNote Rich Context entry (content + expiry)
type BusinessNote struct {
	Content   string `json:"content"`
	ExpiresAt string `json:"expires_at"`
}

// RichContextValue supports two Rich Context value formats
// Can be plain string or BusinessNote struct
type RichContextValue struct {
	BusinessNote
}

// UnmarshalJSON custom JSON parsing, supports string and object
func (r *RichContextValue) UnmarshalJSON(data []byte) error {
	// Try parsing as string
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		r.Content = str
		return nil
	}

	// Try parsing as BusinessNote object
	var note BusinessNote
	if err := json.Unmarshal(data, &note); err != nil {
		return err
	}
	r.BusinessNote = note
	return nil
}

// QualityIssue structured data quality issue
type QualityIssue struct {
	Table       string   `json:"table"`
	Column      string   `json:"column"`
	Type        string   `json:"type"`         // whitespace/type_mismatch/orphan/null_heavy/empty_string
	Severity    string   `json:"severity"`     // critical/warning/info
	Description string   `json:"description"`
	SQLFix      string   `json:"sql_fix"`      // Recommended SQL fix snippet
	AffectedOps []string `json:"affected_ops"` // ["JOIN", "WHERE", "GROUP BY", "ORDER BY"]
	Examples    []string `json:"examples,omitempty"`
}

// ValueStats column value statistics
type ValueStats struct {
	DistinctCount int              `json:"distinct_count"`
	NullCount     int              `json:"null_count"`
	NullPercent   float64          `json:"null_percent"`
	EmptyCount    int              `json:"empty_count,omitempty"`    // For TEXT columns: count of ''
	TopValues     []ValueFrequency `json:"top_values,omitempty"`    // Enumeration values (distinct < 30)
	Range         *NumericRange    `json:"range,omitempty"`         // For numeric columns
}

// ValueFrequency value with frequency
type ValueFrequency struct {
	Value   string  `json:"value"`
	Count   int     `json:"count"`
	Percent float64 `json:"percent"`
}

// NumericRange numeric value range
type NumericRange struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
	Avg float64 `json:"avg"`
}

// TableMetadata table metadata
type TableMetadata struct {
	Name        string                      `json:"name"`
	Comment     string                      `json:"comment,omitempty"`
	Description string                      `json:"description,omitempty"` // Table business description (LLM-generated)
	RowCount    int64                       `json:"row_count"`
	PrimaryKey  []string                    `json:"primary_key,omitempty"` // Primary key column list
	Columns     []ColumnMetadata            `json:"columns"`
	Indexes     []IndexMetadata             `json:"indexes"`
	ForeignKeys []ForeignKeyMetadata        `json:"foreign_keys,omitempty"` // Foreign key relationships
	RichContext map[string]RichContextValue `json:"rich_context,omitempty"`

	// Structured quality issues (deterministic, not LLM-generated)
	QualityIssues []QualityIssue `json:"quality_issues,omitempty"`
}

// ColumnMetadata column metadata
type ColumnMetadata struct {
	Name         string      `json:"name"`
	Type         string      `json:"type"`
	Comment      string      `json:"comment,omitempty"` // Column comment from DDL
	Nullable     bool        `json:"nullable"`
	DefaultValue string      `json:"default,omitempty"`
	IsPrimaryKey bool        `json:"is_primary_key,omitempty"`
	ValueStats   *ValueStats `json:"value_stats,omitempty"` // Deterministic value statistics
}

// IndexMetadata index metadata
type IndexMetadata struct {
	Name      string   `json:"name"`
	Columns   []string `json:"columns"`
	IsUnique  bool     `json:"is_unique,omitempty"`
	IsPrimary bool     `json:"is_primary,omitempty"`
}

// ForeignKeyMetadata foreign key metadata
type ForeignKeyMetadata struct {
	ColumnName       string `json:"column_name"`       // Local column name
	ReferencedTable  string `json:"referenced_table"`  // Referenced table name
	ReferencedColumn string `json:"referenced_column"` // Referenced column name
}

// JoinPath JOIN path info
type JoinPath struct {
	FromTable   string   `json:"from_table"`   // Source table
	ToTable     string   `json:"to_table"`     // Target table
	Path        []string `json:"path"`         // Full path (including intermediate tables)
	JoinClauses []string `json:"join_clauses"` // JOIN clause list
	Description string   `json:"description"`  // Path description
}

// FieldSemantic field semantic info
type FieldSemantic struct {
	TableName   string `json:"table_name"`           // Table name
	ColumnName  string `json:"column_name"`          // Column name
	StorageType string `json:"storage_type"`         // Storage type: foreign_key, name, id, etc.
	References  string `json:"references,omitempty"` // Referenced table.column
	Note        string `json:"note"`                 // Semantic note
}

// NewSharedContext creates shared context
func NewSharedContext(dbName, dbType string) *SharedContext {
	return &SharedContext{
		DatabaseName: dbName,
		DatabaseType: dbType,
		CollectedAt:  time.Now(),
		Tables:       make(map[string]*TableMetadata),
		tasks:        make(map[string]*TaskInfo),
		tempData:     make(map[string]interface{}),
	}
}

// LoadSchemaFromFile loads table structure from schema.sql
func (c *SharedContext) LoadSchemaFromFile(schemaPath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	parser := NewSchemaParser(schemaPath)
	parsedTables, err := parser.Parse()
	if err != nil {
		return fmt.Errorf("failed to parse schema file: %w", err)
	}

	// Convert to TableMetadata
	for tableName, parsedTable := range parsedTables {
		table := &TableMetadata{
			Name:        tableName,
			PrimaryKey:  parsedTable.PrimaryKeys, // Add primary key list
			Columns:     []ColumnMetadata{},
			Indexes:     []IndexMetadata{},
			ForeignKeys: []ForeignKeyMetadata{},
			RichContext: make(map[string]RichContextValue),
		}

		// Convert column info
		for colName, colType := range parsedTable.Columns {
			col := ColumnMetadata{
				Name:     colName,
				Type:     colType,
				Nullable: true, // Default nullable, SQLite characteristic
			}

			// Check if primary key
			for _, pk := range parsedTable.PrimaryKeys {
				if pk == colName {
					col.IsPrimaryKey = true
					break
				}
			}

			table.Columns = append(table.Columns, col)
		}

		// Convert foreign key info
		for _, fk := range parsedTable.ForeignKeys {
			table.ForeignKeys = append(table.ForeignKeys, ForeignKeyMetadata{
				ColumnName:       fk.ColumnName,
				ReferencedTable:  fk.ReferencedTable,
				ReferencedColumn: fk.ReferencedColumn,
			})
		}

		// If table exists (from DB query), merge info
		if existingTable, exists := c.Tables[tableName]; exists {
			// Keep RowCount and RichContext
			table.RowCount = existingTable.RowCount
			table.Description = existingTable.Description
			table.RichContext = existingTable.RichContext
		}

		c.Tables[tableName] = table
	}

	if !c.Quiet {
		fmt.Printf("[Context] Loaded schema from file: %d tables\n", len(parsedTables))
	}
	return nil
}

// RegisterTask registers task
func (c *SharedContext) RegisterTask(taskID, agentID, description string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.tasks[taskID]; exists {
		return fmt.Errorf("task already registered: %s", taskID)
	}

	c.tasks[taskID] = &TaskInfo{
		ID:          taskID,
		AgentID:     agentID,
		Description: description,
		Status:      TaskRegistered,
	}

	if !c.Quiet {
		fmt.Printf("[Context] Task registered: %s by %s\n", taskID, agentID)
	}
	return nil
}

// StartTask marks task started
func (c *SharedContext) StartTask(taskID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	task, exists := c.tasks[taskID]
	if !exists {
		return fmt.Errorf("task not found: %s", taskID)
	}

	if task.Status != TaskRegistered {
		return fmt.Errorf("task %s is not in REGISTERED state", taskID)
	}

	task.Status = TaskRunning
	task.StartTime = time.Now()

	if !c.Quiet {
		fmt.Printf("[Context] Task started: %s\n", taskID)
	}
	return nil
}

// CompleteTask marks task completed
func (c *SharedContext) CompleteTask(taskID string, result map[string]interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	task, exists := c.tasks[taskID]
	if !exists {
		return fmt.Errorf("task not found: %s", taskID)
	}

	task.Status = TaskCompleted
	task.EndTime = time.Now()
	task.Result = result

	duration := task.EndTime.Sub(task.StartTime)
	if !c.Quiet {
		fmt.Printf("[Context] Task completed: %s (took %v)\n", taskID, duration)
	}
	return nil
}

// FailTask marks task failed
func (c *SharedContext) FailTask(taskID string, err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	task, exists := c.tasks[taskID]
	if !exists {
		return fmt.Errorf("task not found: %s", taskID)
	}

	task.Status = TaskFailed
	task.EndTime = time.Now()
	task.Error = err.Error()

	if !c.Quiet {
		fmt.Printf("[Context] Task failed: %s - %v\n", taskID, err)
	}
	return nil
}

// SetData sets temp data
func (c *SharedContext) SetData(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tempData[key] = value
}

// SetTableRichContext sets table Rich Context
// key determined by LLM, e.g.:"status_enum_meaning", "business_rules" etc.
func (c *SharedContext) SetTableRichContext(tableName, key, content, expiresAt string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	table, exists := c.Tables[tableName]
	if !exists {
		// Auto-create table entry if not yet registered (e.g. during Phase 1 ReAct loop)
		table = &TableMetadata{
			Name:        tableName,
			Columns:     []ColumnMetadata{},
			Indexes:     []IndexMetadata{},
			ForeignKeys: []ForeignKeyMetadata{},
			RichContext: make(map[string]RichContextValue),
		}
		c.Tables[tableName] = table
	}

	if table.RichContext == nil {
		table.RichContext = make(map[string]RichContextValue)
	}

	table.RichContext[key] = RichContextValue{
		BusinessNote: BusinessNote{
			Content:   content,
			ExpiresAt: expiresAt,
		},
	}
	return nil
}

// SetTableDescription sets table business description
func (c *SharedContext) SetTableDescription(tableName, description string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	table, exists := c.Tables[tableName]
	if !exists {
		// Auto-create table entry if not yet registered
		table = &TableMetadata{
			Name:        tableName,
			Columns:     []ColumnMetadata{},
			Indexes:     []IndexMetadata{},
			ForeignKeys: []ForeignKeyMetadata{},
			RichContext: make(map[string]RichContextValue),
		}
		c.Tables[tableName] = table
	}

	table.Description = description
	return nil
}

// SetTableQualityIssues sets structured quality issues for a table
func (c *SharedContext) SetTableQualityIssues(tableName string, issues []QualityIssue) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	table, exists := c.Tables[tableName]
	if !exists {
		return fmt.Errorf("table not found: %s", tableName)
	}

	table.QualityIssues = issues
	return nil
}

// SetColumnValueStats sets value statistics for a column
func (c *SharedContext) SetColumnValueStats(tableName, columnName string, stats *ValueStats) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	table, exists := c.Tables[tableName]
	if !exists {
		return fmt.Errorf("table not found: %s", tableName)
	}

	for i, col := range table.Columns {
		if col.Name == columnName {
			table.Columns[i].ValueStats = stats
			return nil
		}
	}

	return fmt.Errorf("column not found: %s.%s", tableName, columnName)
}

// GetData gets data
func (c *SharedContext) GetData(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.tempData[key]
	return val, ok
}

// GetAllData gets all data (read-only copy)
func (c *SharedContext) GetAllData() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Return copy
	copy := make(map[string]interface{})
	for k, v := range c.tempData {
		copy[k] = v
	}
	return copy
}

// GetTaskStatus gets task status
func (c *SharedContext) GetTaskStatus(taskID string) (TaskStatus, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	task, exists := c.tasks[taskID]
	if !exists {
		return 0, fmt.Errorf("task not found: %s", taskID)
	}

	return task.Status, nil
}

// GetAllTasks gets all tasks
func (c *SharedContext) GetAllTasks() []*TaskInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	tasks := make([]*TaskInfo, 0, len(c.tasks))
	for _, task := range c.tasks {
		tasks = append(tasks, task)
	}
	return tasks
}

// IsAllTasksCompleted checks if all tasks completed
func (c *SharedContext) IsAllTasksCompleted() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, task := range c.tasks {
		if task.Status != TaskCompleted {
			return false
		}
	}
	return len(c.tasks) > 0
}

// GetSummary gets summary (for agent awareness)
func (c *SharedContext) GetSummary() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	summary := "=== Shared Context Summary ===\n\n"
	summary += fmt.Sprintf("Database: %s (%s)\n\n", c.DatabaseName, c.DatabaseType)

	// Task statistics
	var registered, running, completed, failed int
	for _, task := range c.tasks {
		switch task.Status {
		case TaskRegistered:
			registered++
		case TaskRunning:
			running++
		case TaskCompleted:
			completed++
		case TaskFailed:
			failed++
		}
	}

	summary += "Tasks:\n"
	summary += fmt.Sprintf("  Total: %d\n", len(c.tasks))
	summary += fmt.Sprintf("  Completed: %d\n", completed)
	summary += fmt.Sprintf("  Running: %d\n", running)
	summary += fmt.Sprintf("  Registered: %d\n", registered)
	summary += fmt.Sprintf("  Failed: %d\n\n", failed)

	// Task list
	if len(c.tasks) > 0 {
		summary += "Task List:\n"
		for _, task := range c.tasks {
			symbol := getStatusSymbol(task.Status)
			summary += fmt.Sprintf("  %s %s - %s (by %s)\n",
				symbol, task.ID, task.Status, task.AgentID)
		}
		summary += "\n"
	}

	// Data summary
	if len(c.tempData) > 0 {
		summary += fmt.Sprintf("Data Keys: %d\n", len(c.tempData))
		for key := range c.tempData {
			summary += fmt.Sprintf("  - %s\n", key)
		}
	}

	return summary
}

// LoadContextFromFile loads SharedContext from file
func LoadContextFromFile(filepath string) (*SharedContext, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, err
	}

	var ctx SharedContext
	if err := json.Unmarshal(data, &ctx); err != nil {
		return nil, err
	}

	return &ctx, nil
}

// SaveToFile saves metadata to file (excludes tasks and tempData)
func (c *SharedContext) SaveToFile(filepath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Build Tables from tempData if not built yet
	if len(c.Tables) == 0 && len(c.tempData) > 0 {
		c.buildTablesFromTempData()
	}

	// Generate Mermaid ER diagram
	if len(c.Tables) > 0 {
		c.SchemaDiagram = c.GenerateMermaidER()
	}

	// Save clean metadata only
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath, data, 0644)
}

// BuildTableMetadata builds metadata for single table (called after Phase 1)
func (c *SharedContext) BuildTableMetadata(tableName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if table exists (from LoadSchemaFromFile)
	table, exists := c.Tables[tableName]
	if !exists {
		// If not exists, create new table
		table = &TableMetadata{
			Name:        tableName,
			Columns:     []ColumnMetadata{},
			Indexes:     []IndexMetadata{},
			ForeignKeys: []ForeignKeyMetadata{},
			RichContext: make(map[string]RichContextValue),
		}
	} else {
		// If exists, keep ForeignKeys but reset others
		// Worker Agent re-queries DB for latest info
		foreignKeys := table.ForeignKeys // Keep foreign keys
		table.Columns = []ColumnMetadata{}
		table.Indexes = []IndexMetadata{}
		table.ForeignKeys = foreignKeys
		if table.RichContext == nil {
			table.RichContext = make(map[string]RichContextValue)
		}
	}

	// Parse column info
	if columnsData, ok := c.tempData[tableName+"_columns"]; ok {
		switch cols := columnsData.(type) {
		case []interface{}:
			for _, colData := range cols {
				if colMap, ok := colData.(map[string]interface{}); ok {
					col := parseColumnMetadata(colMap, c.DatabaseType)
					table.Columns = append(table.Columns, col)
				}
			}
		case []map[string]interface{}:
			for _, colMap := range cols {
				col := parseColumnMetadata(colMap, c.DatabaseType)
				table.Columns = append(table.Columns, col)
			}
		}
	}

	// Parse index info
	if indexesData, ok := c.tempData[tableName+"_indexes"]; ok {
		indexMap := make(map[string]*IndexMetadata)
		switch idxs := indexesData.(type) {
		case []interface{}:
			for _, idxData := range idxs {
				if idxMap, ok := idxData.(map[string]interface{}); ok {
					keyName := getString(idxMap, "Key_name")
					if keyName == "" {
						continue
					}
					if _, exists := indexMap[keyName]; !exists {
						indexMap[keyName] = &IndexMetadata{
							Name:      keyName,
							Columns:   []string{},
							IsPrimary: keyName == "PRIMARY",
						}
						if nonUnique, ok := idxMap["Non_unique"]; ok {
							if nu, ok := nonUnique.(float64); ok {
								indexMap[keyName].IsUnique = (nu == 0)
							}
						}
					}
					if colName := getString(idxMap, "Column_name"); colName != "" {
						indexMap[keyName].Columns = append(indexMap[keyName].Columns, colName)
					}
				}
			}
		case []map[string]interface{}:
			for _, idxMap := range idxs {
				keyName := getString(idxMap, "Key_name")
				if keyName == "" {
					continue
				}
				if _, exists := indexMap[keyName]; !exists {
					indexMap[keyName] = &IndexMetadata{
						Name:      keyName,
						Columns:   []string{},
						IsPrimary: keyName == "PRIMARY",
					}
					if nonUnique, ok := idxMap["Non_unique"]; ok {
						if nu, ok := nonUnique.(float64); ok {
							indexMap[keyName].IsUnique = (nu == 0)
						} else if nu, ok := nonUnique.(int64); ok {
							indexMap[keyName].IsUnique = (nu == 0)
						}
					}
				}
				if colName := getString(idxMap, "Column_name"); colName != "" {
					indexMap[keyName].Columns = append(indexMap[keyName].Columns, colName)
				}
			}
		}
		for _, idx := range indexMap {
			table.Indexes = append(table.Indexes, *idx)
		}
	}

	// Parse FK info
	if foreignKeysData, ok := c.tempData[tableName+"_foreignkeys"]; ok {
		switch fks := foreignKeysData.(type) {
		case []interface{}:
			for _, fkData := range fks {
				if fkMap, ok := fkData.(map[string]interface{}); ok {
					table.ForeignKeys = append(table.ForeignKeys, parseForeignKeyMetadata(fkMap, c.DatabaseType))
				}
			}
		case []map[string]interface{}:
			for _, fkMap := range fks {
				table.ForeignKeys = append(table.ForeignKeys, parseForeignKeyMetadata(fkMap, c.DatabaseType))
			}
		}
	}

	// Parse row count
	if rowcountData, ok := c.tempData[tableName+"_rowcount"]; ok {
		switch rows := rowcountData.(type) {
		case []interface{}:
			if len(rows) > 0 {
				if rowMap, ok := rows[0].(map[string]interface{}); ok {
					if count, ok := rowMap["COUNT(*)"]; ok {
						if c, ok := count.(float64); ok {
							table.RowCount = int64(c)
						} else if c, ok := count.(int64); ok {
							table.RowCount = c
						}
					}
				}
			}
		case []map[string]interface{}:
			if len(rows) > 0 {
				if count, ok := rows[0]["COUNT(*)"]; ok {
					if c, ok := count.(float64); ok {
						table.RowCount = int64(c)
					} else if c, ok := count.(int64); ok {
						table.RowCount = c
					}
				}
			}
		}
	}

	c.Tables[tableName] = table
	if !c.Quiet {
		fmt.Printf("[Context] Built metadata for table: %s (%d columns, %d indexes, %d rows)\n",
			tableName, len(table.Columns), len(table.Indexes), table.RowCount)
	}
}

// buildTablesFromTempData builds Tables structure from temp data
func (c *SharedContext) buildTablesFromTempData() {
	// Extract tempData keys for debug
	keys := make([]string, 0, len(c.tempData))
	for k := range c.tempData {
		keys = append(keys, k)
	}
	if !c.Quiet {
		fmt.Printf("[Context] Building tables from tempData, keys: %v\n", keys)
	}

	// Extract all table names
	tableNames := make(map[string]bool)
	for key := range c.tempData {
		if len(key) > 8 && key[len(key)-8:] == "_columns" {
			tableName := key[:len(key)-8]
			tableNames[tableName] = true
		} else if len(key) > 8 && key[len(key)-8:] == "_indexes" {
			tableName := key[:len(key)-8]
			tableNames[tableName] = true
		} else if len(key) > 9 && key[len(key)-9:] == "_rowcount" {
			tableName := key[:len(key)-9]
			tableNames[tableName] = true
		}
	}

	if !c.Quiet {
		fmt.Printf("[Context] Found tables: %v\n", tableNames)
	}

	// Build metadata for each table
	for tableName := range tableNames {
		table := &TableMetadata{
			Name:        tableName,
			Columns:     []ColumnMetadata{},
			Indexes:     []IndexMetadata{},
			RichContext: make(map[string]RichContextValue),
		}

		// Parse column info
		if columnsData, ok := c.tempData[tableName+"_columns"]; ok {
			// Try two types: []interface{} and []map[string]interface{}
			switch cols := columnsData.(type) {
			case []interface{}:
				for _, colData := range cols {
					if colMap, ok := colData.(map[string]interface{}); ok {
						col := ColumnMetadata{
							Name:     getString(colMap, "Field"),
							Type:     getString(colMap, "Type"),
							Nullable: getString(colMap, "Null") == "YES",
						}
						if def := colMap["Default"]; def != nil {
							col.DefaultValue = fmt.Sprintf("%v", def)
						}
						if getString(colMap, "Key") == "PRI" {
							col.IsPrimaryKey = true
						}
						table.Columns = append(table.Columns, col)
					}
				}
			case []map[string]interface{}:
				for _, colMap := range cols {
					col := ColumnMetadata{
						Name:     getString(colMap, "Field"),
						Type:     getString(colMap, "Type"),
						Nullable: getString(colMap, "Null") == "YES",
					}
					if def := colMap["Default"]; def != nil {
						col.DefaultValue = fmt.Sprintf("%v", def)
					}
					if getString(colMap, "Key") == "PRI" {
						col.IsPrimaryKey = true
					}
					table.Columns = append(table.Columns, col)
				}
			}
		}

		// Parse index info
		if indexesData, ok := c.tempData[tableName+"_indexes"]; ok {
			indexMap := make(map[string]*IndexMetadata)
			switch idxs := indexesData.(type) {
			case []interface{}:
				for _, idxData := range idxs {
					if idxMap, ok := idxData.(map[string]interface{}); ok {
						keyName := getString(idxMap, "Key_name")
						if keyName == "" {
							continue
						}
						if _, exists := indexMap[keyName]; !exists {
							indexMap[keyName] = &IndexMetadata{
								Name:      keyName,
								Columns:   []string{},
								IsPrimary: keyName == "PRIMARY",
							}
							if nonUnique, ok := idxMap["Non_unique"]; ok {
								if nu, ok := nonUnique.(float64); ok {
									indexMap[keyName].IsUnique = (nu == 0)
								}
							}
						}
						if colName := getString(idxMap, "Column_name"); colName != "" {
							indexMap[keyName].Columns = append(indexMap[keyName].Columns, colName)
						}
					}
				}
			case []map[string]interface{}:
				for _, idxMap := range idxs {
					keyName := getString(idxMap, "Key_name")
					if keyName == "" {
						continue
					}
					if _, exists := indexMap[keyName]; !exists {
						indexMap[keyName] = &IndexMetadata{
							Name:      keyName,
							Columns:   []string{},
							IsPrimary: keyName == "PRIMARY",
						}
						if nonUnique, ok := idxMap["Non_unique"]; ok {
							if nu, ok := nonUnique.(float64); ok {
								indexMap[keyName].IsUnique = (nu == 0)
							} else if nu, ok := nonUnique.(int64); ok {
								indexMap[keyName].IsUnique = (nu == 0)
							}
						}
					}
					if colName := getString(idxMap, "Column_name"); colName != "" {
						indexMap[keyName].Columns = append(indexMap[keyName].Columns, colName)
					}
				}
			}
			for _, idx := range indexMap {
				table.Indexes = append(table.Indexes, *idx)
			}
		}

		// Parse row count
		if rowcountData, ok := c.tempData[tableName+"_rowcount"]; ok {
			switch rows := rowcountData.(type) {
			case []interface{}:
				if len(rows) > 0 {
					if rowMap, ok := rows[0].(map[string]interface{}); ok {
						if count, ok := rowMap["COUNT(*)"]; ok {
							if c, ok := count.(float64); ok {
								table.RowCount = int64(c)
							} else if c, ok := count.(int64); ok {
								table.RowCount = c
							}
						}
					}
				}
			case []map[string]interface{}:
				if len(rows) > 0 {
					if count, ok := rows[0]["COUNT(*)"]; ok {
						if c, ok := count.(float64); ok {
							table.RowCount = int64(c)
						} else if c, ok := count.(int64); ok {
							table.RowCount = c
						}
					}
				}
			}
		}

		c.Tables[tableName] = table
		c.TotalRows += table.RowCount
	}

	c.TotalTables = len(c.Tables)
}

// parseColumnMetadata parses column metadata (supports different DB formats)
func parseColumnMetadata(colMap map[string]interface{}, dbType string) ColumnMetadata {
	col := ColumnMetadata{}

	// Normalize DB type to lowercase
	normalizedType := strings.ToLower(dbType)

	switch normalizedType {
	case "sqlite":
		// SQLite PRAGMA table_info() format: cid|name|type|notnull|dflt_value|pk
		col.Name = getString(colMap, "name")
		col.Type = getString(colMap, "type")
		col.Nullable = getInt(colMap, "notnull") == 0 // SQLite: 0=nullable, 1=not null

		if def := colMap["dflt_value"]; def != nil {
			col.DefaultValue = fmt.Sprintf("%v", def)
		}

		col.IsPrimaryKey = getInt(colMap, "pk") > 0

	case "postgresql":
		// PostgreSQL information_schema.columns format
		col.Name = getString(colMap, "column_name")
		col.Type = getString(colMap, "data_type")
		col.Nullable = getString(colMap, "is_nullable") == "YES"

		if def := colMap["column_default"]; def != nil {
			col.DefaultValue = fmt.Sprintf("%v", def)
		}

	case "mysql":
		// MySQL DESCRIBE format: Field|Type|Null|Key|Default|Extra|Comment
		col.Name = getString(colMap, "Field")
		col.Type = getString(colMap, "Type")
		col.Comment = getString(colMap, "Comment")
		col.Nullable = getString(colMap, "Null") == "YES"

		if def := colMap["Default"]; def != nil {
			col.DefaultValue = fmt.Sprintf("%v", def)
		}

		if getString(colMap, "Key") == "PRI" {
			col.IsPrimaryKey = true
		}

	default:
		// Unknown DB type, try generic parsing
		col.Name = getString(colMap, "name")
		if col.Name == "" {
			col.Name = getString(colMap, "Field")
		}
		col.Type = getString(colMap, "type")
		if col.Type == "" {
			col.Type = getString(colMap, "Type")
		}
	}

	return col
}

// parseForeignKeyMetadata parses FK metadata (supports different DB formats)
func parseForeignKeyMetadata(fkMap map[string]interface{}, dbType string) ForeignKeyMetadata {
	fk := ForeignKeyMetadata{}

	normalizedType := strings.ToLower(dbType)

	switch normalizedType {
	case "sqlite":
		// PRAGMA foreign_key_list() format: from, table, to
		fk.ColumnName = getString(fkMap, "from")
		fk.ReferencedTable = getString(fkMap, "table")
		fk.ReferencedColumn = getString(fkMap, "to")

	case "postgresql":
		// information_schema query format
		fk.ColumnName = getString(fkMap, "column_name")
		fk.ReferencedTable = getString(fkMap, "foreign_table_name")
		fk.ReferencedColumn = getString(fkMap, "foreign_column_name")

	case "mysql":
		// SHOW CREATE TABLE format requires string parsing
		if createStmt, ok := fkMap["Create Table"].(string); ok {
			// This is a simplified parser, may need stronger regex
			lines := strings.Split(createStmt, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "CONSTRAINT") && strings.Contains(line, "FOREIGN KEY") {
					parts := strings.Split(line, "`")
					if len(parts) >= 8 {
						// CONSTRAINT `fk_name` FOREIGN KEY (`col_name`) REFERENCES `ref_table` (`ref_col`)
						fk.ColumnName = parts[3]
						fk.ReferencedTable = parts[5]
						fk.ReferencedColumn = parts[7]
						// Note: table may have multiple FKs, this returns first found.
						// Since our agent processes one table at a time, this simplification is acceptable.
					}
				}
			}
		}
	}

	return fk
}

// getStatusSymbol gets task status symbol
func getStatusSymbol(status TaskStatus) string {
	switch status {
	case TaskRegistered:
		return "⏳"
	case TaskRunning:
		return "🔄"
	case TaskCompleted:
		return "✓"
	case TaskFailed:
		return "✗"
	default:
		return "?"
	}
}
