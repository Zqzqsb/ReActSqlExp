package context

import (
	"fmt"
	"strings"
)

// AnalyzeJoinPaths analyzes JOIN paths between tables
func (c *SharedContext) AnalyzeJoinPaths() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// No longer generates verbose JOIN path and field semantic info
	// This info bloats Rich Context and distracts attention
	// Foreign key relationships are sufficient for JOIN info
	// LLM can infer JOIN paths from foreign key relationships

	// Keep data structure init to avoid nil pointers
	if c.JoinPaths == nil {
		c.JoinPaths = make(map[string]*JoinPath)
	}
	if c.FieldSemantics == nil {
		c.FieldSemantics = make(map[string]*FieldSemantic)
	}
}

// buildForeignKeyGraph builds FK relationship graph
func (c *SharedContext) buildForeignKeyGraph() map[string][]string {
	graph := make(map[string][]string)

	for tableName, table := range c.Tables {
		if _, exists := graph[tableName]; !exists {
			graph[tableName] = []string{}
		}

		for _, fk := range table.ForeignKeys {
			// Add bidirectional edges (JOIN works both ways)
			graph[tableName] = append(graph[tableName], fk.ReferencedTable)
			if _, exists := graph[fk.ReferencedTable]; !exists {
				graph[fk.ReferencedTable] = []string{}
			}
			graph[fk.ReferencedTable] = append(graph[fk.ReferencedTable], tableName)
		}
	}

	return graph
}

// findShortestPath uses BFS to find shortest path
func (c *SharedContext) findShortestPath(graph map[string][]string, from, to string) []string {
	if from == to {
		return []string{from}
	}

	visited := make(map[string]bool)
	queue := [][]string{{from}}
	visited[from] = true

	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]

		current := path[len(path)-1]

		for _, neighbor := range graph[current] {
			if neighbor == to {
				return append(path, neighbor)
			}

			if !visited[neighbor] {
				visited[neighbor] = true
				newPath := make([]string, len(path))
				copy(newPath, path)
				newPath = append(newPath, neighbor)
				queue = append(queue, newPath)
			}
		}
	}

	return nil // no path found
}

// buildJoinPath builds JoinPath from path
func (c *SharedContext) buildJoinPath(path []string) *JoinPath {
	if len(path) < 2 {
		return nil
	}

	joinClauses := []string{}
	description := ""

	for i := 0; i < len(path)-1; i++ {
		fromTable := path[i]
		toTable := path[i+1]

		// Find join condition
		joinClause := c.findJoinClause(fromTable, toTable)
		if joinClause != "" {
			joinClauses = append(joinClauses, joinClause)
		}
	}

	// Generate description
	if len(path) == 2 {
		description = fmt.Sprintf("Direct join between %s and %s", path[0], path[1])
	} else {
		intermediates := path[1 : len(path)-1]
		description = fmt.Sprintf("Join through intermediate table(s): %s", strings.Join(intermediates, ", "))
	}

	return &JoinPath{
		FromTable:   path[0],
		ToTable:     path[len(path)-1],
		Path:        path,
		JoinClauses: joinClauses,
		Description: description,
	}
}

// findJoinClause finds JOIN condition between two tables
func (c *SharedContext) findJoinClause(table1, table2 string) string {
	// Check if table1 has FK to table2
	if t1, exists := c.Tables[table1]; exists {
		for _, fk := range t1.ForeignKeys {
			if fk.ReferencedTable == table2 {
				return fmt.Sprintf("%s.%s = %s.%s",
					table1, fk.ColumnName,
					table2, fk.ReferencedColumn)
			}
		}
	}

	// Check if table2 has FK to table1
	if t2, exists := c.Tables[table2]; exists {
		for _, fk := range t2.ForeignKeys {
			if fk.ReferencedTable == table1 {
				return fmt.Sprintf("%s.%s = %s.%s",
					table2, fk.ColumnName,
					table1, fk.ReferencedColumn)
			}
		}
	}

	return ""
}

// reversePath reverses path
func (c *SharedContext) reversePath(path []string) []string {
	reversed := make([]string, len(path))
	for i := 0; i < len(path); i++ {
		reversed[i] = path[len(path)-1-i]
	}
	return reversed
}

// analyzeFieldSemantics analyzes field semantics
func (c *SharedContext) analyzeFieldSemantics() {
	for tableName, table := range c.Tables {
		// Analyze FK fields
		for _, fk := range table.ForeignKeys {
			key := fmt.Sprintf("%s.%s", tableName, fk.ColumnName)
			c.FieldSemantics[key] = &FieldSemantic{
				TableName:   tableName,
				ColumnName:  fk.ColumnName,
				StorageType: "foreign_key",
				References:  fmt.Sprintf("%s.%s", fk.ReferencedTable, fk.ReferencedColumn),
				Note:        fmt.Sprintf("Stores %s ID, not %s name. Use JOIN to get related data.", fk.ReferencedTable, fk.ReferencedTable),
			}
		}

		// Analyze possible ID fields (not FK)
		for _, col := range table.Columns {
			colLower := strings.ToLower(col.Name)
			if strings.HasSuffix(colLower, "_id") || strings.HasSuffix(colLower, "id") {
				key := fmt.Sprintf("%s.%s", tableName, col.Name)

				// Check if already handled as FK
				if _, exists := c.FieldSemantics[key]; !exists {
					if col.IsPrimaryKey {
						c.FieldSemantics[key] = &FieldSemantic{
							TableName:   tableName,
							ColumnName:  col.Name,
							StorageType: "primary_key",
							Note:        fmt.Sprintf("Primary key of %s table", tableName),
						}
					} else {
						c.FieldSemantics[key] = &FieldSemantic{
							TableName:   tableName,
							ColumnName:  col.Name,
							StorageType: "id_field",
							Note:        "Likely an identifier field, may need special handling",
						}
					}
				}
			}
		}
	}
}

// FormatJoinPathsForPrompt formats JOIN path info for Prompt
func (c *SharedContext) FormatJoinPathsForPrompt() string {
	if len(c.JoinPaths) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n## Join Path Guidelines\n")
	sb.WriteString("When joining tables, refer to these pre-analyzed join paths:\n\n")

	// Sort output by table name
	for key, joinPath := range c.JoinPaths {
		sb.WriteString(fmt.Sprintf("**%s**:\n", key))
		sb.WriteString(fmt.Sprintf("  - Path: %s\n", strings.Join(joinPath.Path, " → ")))
		sb.WriteString(fmt.Sprintf("  - Description: %s\n", joinPath.Description))
		if len(joinPath.JoinClauses) > 0 {
			sb.WriteString("  - Join clauses:\n")
			for _, clause := range joinPath.JoinClauses {
				sb.WriteString(fmt.Sprintf("    * %s\n", clause))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// FormatFieldSemanticsForPrompt formats field semantic info for Prompt
func (c *SharedContext) FormatFieldSemanticsForPrompt() string {
	if len(c.FieldSemantics) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n## Field Semantics\n")
	sb.WriteString("Important field storage information:\n\n")

	// Group by table name
	tableFields := make(map[string][]*FieldSemantic)
	for _, fs := range c.FieldSemantics {
		tableFields[fs.TableName] = append(tableFields[fs.TableName], fs)
	}

	for tableName, fields := range tableFields {
		sb.WriteString(fmt.Sprintf("**%s**:\n", tableName))
		for _, fs := range fields {
			sb.WriteString(fmt.Sprintf("  - %s (%s): %s\n", fs.ColumnName, fs.StorageType, fs.Note))
			if fs.References != "" {
				sb.WriteString(fmt.Sprintf("    References: %s\n", fs.References))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
