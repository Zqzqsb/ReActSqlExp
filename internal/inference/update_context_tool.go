package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UpdateRichContextTool tool for updating Rich Context
type UpdateRichContextTool struct {
	dbName      string
	contextPath string
	logger      *InferenceLogger
}

// Name returns tool name
func (t *UpdateRichContextTool) Name() string {
	return "update_rich_context"
}

// Description returns tool description
func (t *UpdateRichContextTool) Description() string {
	return `Update expired or incorrect Rich Context information.
Use this tool when you find an EXPIRED insight is incorrect after verification.

Input should be a JSON object with:
- table_name: string (the table name)
- note_key: string (the Rich Context note key to update)
- new_content: string (the corrected content)
- reason: string (why you're updating it, based on SQL verification)

Example:
{
  "table_name": "cars_data",
  "note_key": "Year_business_meaning",
  "new_content": "Model year (4-digit format, 1970-1982)",
  "reason": "Verified by SQL: SELECT DISTINCT Year shows 4-digit years, not 2-digit"
}`
}

// UpdateInput update parameters
type UpdateInput struct {
	TableName  string `json:"table_name"`
	NoteKey    string `json:"note_key"`
	NewContent string `json:"new_content"`
	Reason     string `json:"reason"`
}

// BusinessNote Rich Context entry
type BusinessNote struct {
	Content   string `json:"content"`
	ExpiresAt string `json:"expires_at"`
}

// Call executes update
func (t *UpdateRichContextTool) Call(ctx context.Context, input string) (string, error) {
	// Clean input: remove possible markdown code blocks
	input = strings.TrimSpace(input)
	input = strings.TrimPrefix(input, "```json")
	input = strings.TrimPrefix(input, "```")
	input = strings.TrimSuffix(input, "```")
	input = strings.TrimSpace(input)

	// Parse input
	var updateInput UpdateInput
	if err := json.Unmarshal([]byte(input), &updateInput); err != nil {
		// Return friendly error, do not interrupt inference
		return fmt.Sprintf("⚠️  Failed to parse input JSON: %v\nPlease provide valid JSON without markdown code blocks.", err), nil
	}

	// Validate parameters
	if updateInput.TableName == "" {
		return "⚠️  Error: table_name is required", nil
	}
	if updateInput.NoteKey == "" {
		return "⚠️  Error: note_key is required", nil
	}
	if updateInput.NewContent == "" {
		return "⚠️  Error: new_content is required", nil
	}

	// Read Rich Context file
	data, err := os.ReadFile(t.contextPath)
	if err != nil {
		return fmt.Sprintf("⚠️  Failed to read context file: %v\nContinue with SQL generation.", err), nil
	}

	// Parse as generic map
	var rawData map[string]interface{}
	if err := json.Unmarshal(data, &rawData); err != nil {
		return fmt.Sprintf("⚠️  Failed to parse context file: %v\nContinue with SQL generation.", err), nil
	}

	// Get tables
	tables, ok := rawData["tables"].(map[string]interface{})
	if !ok {
		return "⚠️  No tables field in context. Continue with SQL generation.", nil
	}

	// Get specified table
	tableData, ok := tables[updateInput.TableName].(map[string]interface{})
	if !ok {
		return fmt.Sprintf("⚠️  Table '%s' not found in context. Continue with SQL generation.", updateInput.TableName), nil
	}

	// Get rich_context
	richContext, ok := tableData["rich_context"].(map[string]interface{})
	if !ok {
		return fmt.Sprintf("⚠️  No rich_context in table '%s'. Continue with SQL generation.", updateInput.TableName), nil
	}

	// Check note existence - create new if not found
	if _, exists := richContext[updateInput.NoteKey]; !exists {
		return fmt.Sprintf("⚠️  Note key '%s' not found in table '%s'.\nTip: This might be a new insight. You can continue with SQL generation based on your findings.", updateInput.NoteKey, updateInput.TableName), nil
	}

	// Update note
	expiresAt := time.Now().Add(7 * 24 * time.Hour).Format(time.RFC3339)
	richContext[updateInput.NoteKey] = map[string]string{
		"content":    updateInput.NewContent,
		"expires_at": expiresAt,
	}

	// Write back to file
	output, err := json.MarshalIndent(rawData, "", "  ")
	if err != nil {
		return fmt.Sprintf("⚠️  Failed to marshal context: %v\nContinue with SQL generation.", err), nil
	}

	if err := os.WriteFile(t.contextPath, output, 0644); err != nil {
		return fmt.Sprintf("⚠️  Failed to write context file: %v\nContinue with SQL generation.", err), nil
	}

	// Return success info
	result := fmt.Sprintf(
		"✓ Rich Context updated successfully!\n"+
			"Table: %s\n"+
			"Note: %s\n"+
			"New Content: %s\n"+
			"Expires At: %s\n"+
			"Reason: %s",
		updateInput.TableName,
		updateInput.NoteKey,
		updateInput.NewContent,
		expiresAt,
		updateInput.Reason,
	)

	logf := func(format string, a ...interface{}) {
		if t.logger != nil {
			t.logger.Printf(format, a...)
		} else {
			fmt.Printf(format, a...)
		}
	}
	logln := func(a ...interface{}) {
		if t.logger != nil {
			t.logger.Println(a...)
		} else {
			fmt.Println(a...)
		}
	}

	logln("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	logln("📝 Rich Context Updated:")
	logln("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	logf("%s\n", result)
	logln("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	return result, nil
}

// NewUpdateRichContextTool creates update tool
func NewUpdateRichContextTool(dbName, dbType string) *UpdateRichContextTool {
	// Build context file path
	contextPath := filepath.Join("contexts", strings.ToLower(dbType), "spider", dbName+".json")

	return &UpdateRichContextTool{
		dbName:      dbName,
		contextPath: contextPath,
	}
}
