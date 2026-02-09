package context

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// SchemaParser parses schema.sql files
type SchemaParser struct {
	filePath string
}

// NewSchemaParser creates parser
func NewSchemaParser(filePath string) *SchemaParser {
	return &SchemaParser{filePath: filePath}
}

// ParsedTable parsed table structure
type ParsedTable struct {
	Name        string
	Columns     map[string]string // column_name -> type
	PrimaryKeys []string
	ForeignKeys []ParsedForeignKey
}

// ParsedForeignKey parsed foreign key
type ParsedForeignKey struct {
	ColumnName       string
	ReferencedTable  string
	ReferencedColumn string
}

// Parse parses schema.sql file
func (p *SchemaParser) Parse() (map[string]*ParsedTable, error) {
	content, err := os.ReadFile(p.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read schema file: %w", err)
	}

	sql := string(content)

	// Remove comments
	sql = removeComments(sql)

	// Extract all CREATE TABLE statements
	tables := make(map[string]*ParsedTable)

	// Regex match CREATE TABLE
	// Supports quoted and unquoted table names
	createTableRegex := regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?["'\x60]?(\w+)["'\x60]?\s*\(((?:[^()]|\([^)]*\))*)\)`)

	matches := createTableRegex.FindAllStringSubmatch(sql, -1)

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}

		tableName := match[1] // Preserve original case
		tableBody := match[2]

		table := &ParsedTable{
			Name:        strings.ToLower(tableName), // Table name lowercase for lookup
			Columns:     make(map[string]string),
			PrimaryKeys: []string{},
			ForeignKeys: []ParsedForeignKey{},
		}

		// Parse table body
		parseTableBody(table, tableBody)

		tables[tableName] = table
	}

	return tables, nil
}

// removeComments removes SQL comments
func removeComments(sql string) string {
	// Remove single-line comments --
	lineCommentRegex := regexp.MustCompile(`--[^\n]*`)
	sql = lineCommentRegex.ReplaceAllString(sql, "")

	// Remove multi-line comments /* */
	blockCommentRegex := regexp.MustCompile(`/\*[\s\S]*?\*/`)
	sql = blockCommentRegex.ReplaceAllString(sql, "")

	return sql
}

// parseTableBody parses table definition body
func parseTableBody(table *ParsedTable, body string) {
	// Split into definition items (columns, constraints)
	items := splitTableItems(body)

	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		itemLower := strings.ToLower(item)

		// Check if primary key constraint
		if strings.HasPrefix(itemLower, "primary key") {
			parsePrimaryKey(table, item)
			continue
		}

		// Check if foreign key constraint
		if strings.HasPrefix(itemLower, "foreign key") {
			parseForeignKey(table, item)
			continue
		}

		// Otherwise column definition
		parseColumnDefinition(table, item)
	}
}

// splitTableItems splits table items (handles nested parens)
func splitTableItems(body string) []string {
	var items []string
	var current strings.Builder
	parenDepth := 0

	for _, ch := range body {
		switch ch {
		case '(':
			parenDepth++
			current.WriteRune(ch)
		case ')':
			parenDepth--
			current.WriteRune(ch)
		case ',':
			if parenDepth == 0 {
				items = append(items, current.String())
				current.Reset()
			} else {
				current.WriteRune(ch)
			}
		default:
			current.WriteRune(ch)
		}
	}

	if current.Len() > 0 {
		items = append(items, current.String())
	}

	return items
}

// parseColumnDefinition parses column definition
func parseColumnDefinition(table *ParsedTable, def string) {
	// Remove quotes
	def = strings.Trim(def, `"'\x60`)

	// Split column name and type
	parts := strings.Fields(def)
	if len(parts) < 2 {
		return
	}

	columnName := strings.Trim(parts[0], `"'\x60`) // Preserve original case
	columnType := strings.ToUpper(parts[1])

	// Check for primary key keyword (column-level)
	defLower := strings.ToLower(def)
	if strings.Contains(defLower, "primary key") {
		table.PrimaryKeys = append(table.PrimaryKeys, columnName)
	}

	// Check for FK (column-level)
	// Format: REFERENCES table(column)
	if strings.Contains(defLower, "references") {
		parseForeignKeyInline(table, columnName, def)
	}

	table.Columns[columnName] = columnType
}

// parsePrimaryKey parses primary key constraint
func parsePrimaryKey(table *ParsedTable, constraint string) {
	// Extract column names from parens
	// Format: primary key ("col1", "col2")
	re := regexp.MustCompile(`\((.*?)\)`)
	matches := re.FindStringSubmatch(constraint)
	if len(matches) < 2 {
		return
	}

	columnsStr := matches[1]
	columns := strings.Split(columnsStr, ",")

	for _, col := range columns {
		col = strings.TrimSpace(col)
		col = strings.Trim(col, `"'\x60`)
		// Preserve original case
		if col != "" {
			table.PrimaryKeys = append(table.PrimaryKeys, col)
		}
	}
}

// parseForeignKey parses FK constraint (table-level)
func parseForeignKey(table *ParsedTable, constraint string) {
	// Format: foreign key("col") references `table`("ref_col")

	// Extract local column (preserve case)
	colRe := regexp.MustCompile(`(?i)foreign\s+key\s*\(\s*["'\x60]?(\w+)["'\x60]?\s*\)`)
	colMatches := colRe.FindStringSubmatch(constraint)
	if len(colMatches) < 2 {
		return
	}
	columnName := colMatches[1]

	// Extract referenced table and column (preserve case)
	refRe := regexp.MustCompile(`(?i)references\s+["'\x60]?(\w+)["'\x60]?\s*\(\s*["'\x60]?(\w+)["'\x60]?\s*\)`)
	refMatches := refRe.FindStringSubmatch(constraint)
	if len(refMatches) < 3 {
		return
	}

	table.ForeignKeys = append(table.ForeignKeys, ParsedForeignKey{
		ColumnName:       columnName,
		ReferencedTable:  strings.ToLower(refMatches[1]), // Table name lowercase
		ReferencedColumn: refMatches[2],                  // Column name preserves original case
	})
}

// parseForeignKeyInline parses inline FK (column-level)
func parseForeignKeyInline(table *ParsedTable, columnName, def string) {
	// Format: REFERENCES table(column)
	refRe := regexp.MustCompile(`(?i)references\s+["'\x60]?(\w+)["'\x60]?\s*\(\s*["'\x60]?(\w+)["'\x60]?\s*\)`)
	refMatches := refRe.FindStringSubmatch(def)
	if len(refMatches) < 3 {
		return
	}

	table.ForeignKeys = append(table.ForeignKeys, ParsedForeignKey{
		ColumnName:       columnName,
		ReferencedTable:  strings.ToLower(refMatches[1]), // Table name lowercase
		ReferencedColumn: refMatches[2],                  // Column name preserves original case
	})
}
