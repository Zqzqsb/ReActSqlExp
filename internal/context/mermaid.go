package context

import (
	"fmt"
	"strings"
)

// GenerateMermaidER generates Mermaid ER diagram
// Note: caller must already hold the lock
func (c *SharedContext) GenerateMermaidER() *SchemaDiagram {
	// No lock needed, caller already holds it

	var sb strings.Builder

	// ER diagram header
	sb.WriteString("erDiagram\n")

	// Collect all relationships
	relationships := make(map[string]bool) // for dedup

	for _, table := range c.Tables {
		for _, fk := range table.ForeignKeys {
			// Format: TABLE1 ||--o{ TABLE2 : "relationship"
			// ||--o{ represents one-to-many
			refTable := strings.ToUpper(fk.ReferencedTable)
			currentTable := strings.ToUpper(table.Name)

			// Relationship: use column name as label
			relationKey := fmt.Sprintf("%s_%s_%s", refTable, currentTable, fk.ColumnName)

			if !relationships[relationKey] {
				sb.WriteString(fmt.Sprintf("    %s ||--o{ %s : \"has\"\n",
					refTable, currentTable))
				relationships[relationKey] = true
			}
		}
	}

	sb.WriteString("\n")

	// Add table structure definitions
	for _, table := range c.Tables {
		tableName := strings.ToUpper(table.Name)
		sb.WriteString(fmt.Sprintf("    %s {\n", tableName))

		// Add column definitions
		for _, col := range table.Columns {
			// Format: type column_name PK/FK
			var tags []string

			if col.IsPrimaryKey {
				tags = append(tags, "PK")
			}

			// Check if foreign key
			for _, fk := range table.ForeignKeys {
				if fk.ColumnName == col.Name {
					tags = append(tags, "FK")
					break
				}
			}

			tagStr := ""
			if len(tags) > 0 {
				tagStr = " " + strings.Join(tags, ",")
			}

			// Simplify type name
			colType := simplifyType(col.Type)

			sb.WriteString(fmt.Sprintf("        %s %s%s\n",
				colType, col.Name, tagStr))
		}

		sb.WriteString("    }\n")
	}

	return &SchemaDiagram{
		Format:      "mermaid-er",
		Description: "Entity-Relationship diagram showing database schema and relationships",
		Content:     sb.String(),
	}
}

// simplifyType simplifies type names for Mermaid display
func simplifyType(fullType string) string {
	// Remove length limits, keep base type
	fullType = strings.ToLower(fullType)

	if strings.Contains(fullType, "int") {
		return "int"
	}
	if strings.Contains(fullType, "varchar") || strings.Contains(fullType, "char") {
		return "string"
	}
	if strings.Contains(fullType, "text") {
		return "text"
	}
	if strings.Contains(fullType, "real") || strings.Contains(fullType, "float") || strings.Contains(fullType, "double") {
		return "float"
	}
	if strings.Contains(fullType, "date") || strings.Contains(fullType, "time") {
		return "datetime"
	}
	if strings.Contains(fullType, "bool") {
		return "boolean"
	}

	return "string" // Default
}
