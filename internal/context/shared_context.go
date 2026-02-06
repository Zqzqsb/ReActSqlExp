package context

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// TaskStatus 任务状态
type TaskStatus int

const (
	TaskRegistered TaskStatus = iota // 已注册
	TaskRunning                      // 执行中
	TaskCompleted                    // 已完成
	TaskFailed                       // 失败
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

// TaskInfo 任务信息
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

// SchemaDiagram 数据库关系图
type SchemaDiagram struct {
	Format      string `json:"format"`      // "mermaid-er"
	Description string `json:"description"` // 图表描述
	Content     string `json:"content"`     // Mermaid 代码
}

// SharedContext 共享上下文（多Agent协作）
type SharedContext struct {
	// 数据库信息
	DatabaseName string    `json:"database_name"`
	DatabaseType string    `json:"database_type"`
	Version      string    `json:"version,omitempty"`
	CollectedAt  time.Time `json:"collected_at"`

	// Schema 关系图
	SchemaDiagram *SchemaDiagram `json:"schema_diagram,omitempty"`

	// Metadata（干净的数据库元数据）
	Tables      map[string]*TableMetadata `json:"tables"`
	TotalTables int                       `json:"total_tables"`
	TotalRows   int64                     `json:"total_rows"`

	// JOIN 路径分析（新增）
	JoinPaths map[string]*JoinPath `json:"join_paths,omitempty"`

	// 字段语义信息（新增）
	FieldSemantics map[string]*FieldSemantic `json:"field_semantics,omitempty"`

	// 任务注册表（不保存到JSON）
	tasks map[string]*TaskInfo `json:"-"`

	// 临时数据（不保存到JSON）
	tempData map[string]interface{} `json:"-"`

	// 并发控制
	mu sync.RWMutex `json:"-"`
}

// BusinessNote Rich Context 条目（包含内容和过期时间）
type BusinessNote struct {
	Content   string `json:"content"`
	ExpiresAt string `json:"expires_at"`
}

// RichContextValue 支持两种格式的 Rich Context 值
// 可以是简单字符串或 BusinessNote 结构
type RichContextValue struct {
	BusinessNote
}

// UnmarshalJSON 自定义 JSON 解析，支持字符串和对象两种格式
func (r *RichContextValue) UnmarshalJSON(data []byte) error {
	// 尝试解析为字符串
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		r.Content = str
		return nil
	}

	// 尝试解析为 BusinessNote 对象
	var note BusinessNote
	if err := json.Unmarshal(data, &note); err != nil {
		return err
	}
	r.BusinessNote = note
	return nil
}

// TableMetadata 表元数据
type TableMetadata struct {
	Name        string                      `json:"name"`
	Comment     string                      `json:"comment,omitempty"`
	Description string                      `json:"description,omitempty"` // 表的业务描述（LLM生成）
	RowCount    int64                       `json:"row_count"`
	PrimaryKey  []string                    `json:"primary_key,omitempty"` // 主键列名列表
	Columns     []ColumnMetadata            `json:"columns"`
	Indexes     []IndexMetadata             `json:"indexes"`
	ForeignKeys []ForeignKeyMetadata        `json:"foreign_keys,omitempty"` // 外键关系
	RichContext map[string]RichContextValue `json:"rich_context,omitempty"`
}

// ColumnMetadata 列元数据
type ColumnMetadata struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Comment      string `json:"comment,omitempty"` // 从DDL提取的列注释
	Nullable     bool   `json:"nullable"`
	DefaultValue string `json:"default,omitempty"`
	IsPrimaryKey bool   `json:"is_primary_key,omitempty"`
}

// IndexMetadata 索引元数据
type IndexMetadata struct {
	Name      string   `json:"name"`
	Columns   []string `json:"columns"`
	IsUnique  bool     `json:"is_unique,omitempty"`
	IsPrimary bool     `json:"is_primary,omitempty"`
}

// ForeignKeyMetadata 外键元数据
type ForeignKeyMetadata struct {
	ColumnName       string `json:"column_name"`       // 本表的列名
	ReferencedTable  string `json:"referenced_table"`  // 引用的表名
	ReferencedColumn string `json:"referenced_column"` // 引用的列名
}

// JoinPath JOIN 路径信息
type JoinPath struct {
	FromTable   string   `json:"from_table"`   // 起始表
	ToTable     string   `json:"to_table"`     // 目标表
	Path        []string `json:"path"`         // 完整路径（包含中间表）
	JoinClauses []string `json:"join_clauses"` // JOIN 子句列表
	Description string   `json:"description"`  // 路径描述
}

// FieldSemantic 字段语义信息
type FieldSemantic struct {
	TableName   string `json:"table_name"`           // 表名
	ColumnName  string `json:"column_name"`          // 列名
	StorageType string `json:"storage_type"`         // 存储类型：foreign_key, name, id, etc.
	References  string `json:"references,omitempty"` // 引用的表.列
	Note        string `json:"note"`                 // 语义说明
}

// NewSharedContext 创建共享上下文
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

// LoadSchemaFromFile 从 schema.sql 文件加载表结构
func (c *SharedContext) LoadSchemaFromFile(schemaPath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	parser := NewSchemaParser(schemaPath)
	parsedTables, err := parser.Parse()
	if err != nil {
		return fmt.Errorf("failed to parse schema file: %w", err)
	}

	// 转换为 TableMetadata
	for tableName, parsedTable := range parsedTables {
		table := &TableMetadata{
			Name:        tableName,
			PrimaryKey:  parsedTable.PrimaryKeys, // 添加主键列表
			Columns:     []ColumnMetadata{},
			Indexes:     []IndexMetadata{},
			ForeignKeys: []ForeignKeyMetadata{},
			RichContext: make(map[string]RichContextValue),
		}

		// 转换列信息
		for colName, colType := range parsedTable.Columns {
			col := ColumnMetadata{
				Name:     colName,
				Type:     colType,
				Nullable: true, // 默认可空，SQLite 特性
			}

			// 检查是否是主键
			for _, pk := range parsedTable.PrimaryKeys {
				if pk == colName {
					col.IsPrimaryKey = true
					break
				}
			}

			table.Columns = append(table.Columns, col)
		}

		// 转换外键信息
		for _, fk := range parsedTable.ForeignKeys {
			table.ForeignKeys = append(table.ForeignKeys, ForeignKeyMetadata{
				ColumnName:       fk.ColumnName,
				ReferencedTable:  fk.ReferencedTable,
				ReferencedColumn: fk.ReferencedColumn,
			})
		}

		// 如果表已存在（从数据库查询获得），合并信息
		if existingTable, exists := c.Tables[tableName]; exists {
			// 保留 RowCount 和 RichContext
			table.RowCount = existingTable.RowCount
			table.Description = existingTable.Description
			table.RichContext = existingTable.RichContext
		}

		c.Tables[tableName] = table
	}

	fmt.Printf("[Context] Loaded schema from file: %d tables\n", len(parsedTables))
	return nil
}

// RegisterTask 注册任务
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

	fmt.Printf("[Context] Task registered: %s by %s\n", taskID, agentID)
	return nil
}

// StartTask 标记任务开始
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

	fmt.Printf("[Context] Task started: %s\n", taskID)
	return nil
}

// CompleteTask 标记任务完成
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
	fmt.Printf("[Context] Task completed: %s (took %v)\n", taskID, duration)
	return nil
}

// FailTask 标记任务失败
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

	fmt.Printf("[Context] Task failed: %s - %v\n", taskID, err)
	return nil
}

// SetData 设置临时数据
func (c *SharedContext) SetData(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tempData[key] = value
}

// SetTableRichContext 设置表的Rich Context
// key由LLM自主决定，例如："status_enum_meaning", "business_rules"等
func (c *SharedContext) SetTableRichContext(tableName, key, content, expiresAt string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	table, exists := c.Tables[tableName]
	if !exists {
		return fmt.Errorf("table %s not found", tableName)
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

// SetTableDescription 设置表的业务描述
func (c *SharedContext) SetTableDescription(tableName, description string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	table, exists := c.Tables[tableName]
	if !exists {
		return fmt.Errorf("table %s not found", tableName)
	}

	table.Description = description
	return nil
}

// GetData 获取数据
func (c *SharedContext) GetData(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.tempData[key]
	return val, ok
}

// GetAllData 获取所有数据（只读副本）
func (c *SharedContext) GetAllData() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 返回副本
	copy := make(map[string]interface{})
	for k, v := range c.tempData {
		copy[k] = v
	}
	return copy
}

// GetTaskStatus 获取任务状态
func (c *SharedContext) GetTaskStatus(taskID string) (TaskStatus, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	task, exists := c.tasks[taskID]
	if !exists {
		return 0, fmt.Errorf("task not found: %s", taskID)
	}

	return task.Status, nil
}

// GetAllTasks 获取所有任务
func (c *SharedContext) GetAllTasks() []*TaskInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	tasks := make([]*TaskInfo, 0, len(c.tasks))
	for _, task := range c.tasks {
		tasks = append(tasks, task)
	}
	return tasks
}

// IsAllTasksCompleted 检查是否所有任务都完成
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

// GetSummary 获取摘要（用于Agent感知）
func (c *SharedContext) GetSummary() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	summary := "=== Shared Context Summary ===\n\n"
	summary += fmt.Sprintf("Database: %s (%s)\n\n", c.DatabaseName, c.DatabaseType)

	// 任务统计
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

	// 任务列表
	if len(c.tasks) > 0 {
		summary += "Task List:\n"
		for _, task := range c.tasks {
			symbol := getStatusSymbol(task.Status)
			summary += fmt.Sprintf("  %s %s - %s (by %s)\n",
				symbol, task.ID, task.Status, task.AgentID)
		}
		summary += "\n"
	}

	// 数据摘要
	if len(c.tempData) > 0 {
		summary += fmt.Sprintf("Data Keys: %d\n", len(c.tempData))
		for key := range c.tempData {
			summary += fmt.Sprintf("  - %s\n", key)
		}
	}

	return summary
}

// LoadContextFromFile 从文件加载SharedContext
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

// SaveToFile 保存metadata到文件（不包含tasks和tempData）
func (c *SharedContext) SaveToFile(filepath string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 先从tempData构建Tables（如果还没构建）
	if len(c.Tables) == 0 && len(c.tempData) > 0 {
		c.buildTablesFromTempData()
	}

	// 生成 Mermaid ER 图
	if len(c.Tables) > 0 {
		c.SchemaDiagram = c.GenerateMermaidER()
	}

	// 只保存干净的metadata
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath, data, 0644)
}

// BuildTableMetadata 为单个表构建metadata（Phase 1完成后调用）
func (c *SharedContext) BuildTableMetadata(tableName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 检查表是否已存在（从 LoadSchemaFromFile 加载）
	table, exists := c.Tables[tableName]
	if !exists {
		// 如果不存在，创建新表
		table = &TableMetadata{
			Name:        tableName,
			Columns:     []ColumnMetadata{},
			Indexes:     []IndexMetadata{},
			ForeignKeys: []ForeignKeyMetadata{},
			RichContext: make(map[string]RichContextValue),
		}
	} else {
		// 如果已存在，保留 ForeignKeys，但重置其他字段
		// 因为 Worker Agent 会重新查询数据库获取最新信息
		foreignKeys := table.ForeignKeys // 保留外键
		table.Columns = []ColumnMetadata{}
		table.Indexes = []IndexMetadata{}
		table.ForeignKeys = foreignKeys
		if table.RichContext == nil {
			table.RichContext = make(map[string]RichContextValue)
		}
	}

	// 解析列信息
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

	// 解析索引信息
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

	// 解析外键信息
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

	// 解析行数
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
	fmt.Printf("[Context] Built metadata for table: %s (%d columns, %d indexes, %d rows)\n",
		tableName, len(table.Columns), len(table.Indexes), table.RowCount)
}

// buildTablesFromTempData 从临时数据构建Tables结构
func (c *SharedContext) buildTablesFromTempData() {
	// 提取 tempData 的 keys 用于调试
	keys := make([]string, 0, len(c.tempData))
	for k := range c.tempData {
		keys = append(keys, k)
	}
	fmt.Printf("[Context] Building tables from tempData, keys: %v\n", keys)

	// 提取所有表名
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

	fmt.Printf("[Context] Found tables: %v\n", tableNames)

	// 为每个表构建metadata
	for tableName := range tableNames {
		table := &TableMetadata{
			Name:        tableName,
			Columns:     []ColumnMetadata{},
			Indexes:     []IndexMetadata{},
			RichContext: make(map[string]RichContextValue),
		}

		// 解析列信息
		if columnsData, ok := c.tempData[tableName+"_columns"]; ok {
			// 尝试两种类型：[]interface{} 和 []map[string]interface{}
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

		// 解析索引信息
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

		// 解析行数
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

// parseColumnMetadata 解析列元数据（支持不同数据库格式）
func parseColumnMetadata(colMap map[string]interface{}, dbType string) ColumnMetadata {
	col := ColumnMetadata{}

	// 标准化数据库类型为小写
	normalizedType := strings.ToLower(dbType)

	switch normalizedType {
	case "sqlite":
		// SQLite PRAGMA table_info() 格式: cid|name|type|notnull|dflt_value|pk
		col.Name = getString(colMap, "name")
		col.Type = getString(colMap, "type")
		col.Nullable = getInt(colMap, "notnull") == 0 // SQLite: 0=nullable, 1=not null

		if def := colMap["dflt_value"]; def != nil {
			col.DefaultValue = fmt.Sprintf("%v", def)
		}

		col.IsPrimaryKey = getInt(colMap, "pk") > 0

	case "postgresql":
		// PostgreSQL information_schema.columns 格式
		col.Name = getString(colMap, "column_name")
		col.Type = getString(colMap, "data_type")
		col.Nullable = getString(colMap, "is_nullable") == "YES"

		if def := colMap["column_default"]; def != nil {
			col.DefaultValue = fmt.Sprintf("%v", def)
		}

	case "mysql":
		// MySQL DESCRIBE 格式: Field|Type|Null|Key|Default|Extra|Comment
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
		// 未知数据库类型，尝试通用解析
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

// parseForeignKeyMetadata 解析外键元数据（支持不同数据库格式）
func parseForeignKeyMetadata(fkMap map[string]interface{}, dbType string) ForeignKeyMetadata {
	fk := ForeignKeyMetadata{}

	normalizedType := strings.ToLower(dbType)

	switch normalizedType {
	case "sqlite":
		// PRAGMA foreign_key_list() 格式: from, table, to
		fk.ColumnName = getString(fkMap, "from")
		fk.ReferencedTable = getString(fkMap, "table")
		fk.ReferencedColumn = getString(fkMap, "to")

	case "postgresql":
		// information_schema query 格式
		fk.ColumnName = getString(fkMap, "column_name")
		fk.ReferencedTable = getString(fkMap, "foreign_table_name")
		fk.ReferencedColumn = getString(fkMap, "foreign_column_name")

	case "mysql":
		// SHOW CREATE TABLE 格式需要解析字符串
		if createStmt, ok := fkMap["Create Table"].(string); ok {
			// 这是一个简化的解析器，可能需要更强的正则表达式
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
						// 注意：一个表中可能有多个外键，这里只返回第一个找到的。正确的实现应该在外面循环。
						// 但由于我们的agent每次只处理一个表，这个简化是可接受的。
					}
				}
			}
		}
	}

	return fk
}

// getStatusSymbol 获取任务状态符号
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
