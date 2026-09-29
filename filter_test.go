package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEvaluateFilter_EmptyOrMissingType(t *testing.T) {
	record := map[string]interface{}{"command": "SELECT", "dbUserName": "app_user"}

	// Empty query matches all
	if !EvaluateFilter(record, map[string]interface{}{}) {
		t.Errorf("expected empty query to return true")
	}

	// Missing type field matches all
	if !EvaluateFilter(record, map[string]interface{}{"foo": "bar"}) {
		t.Errorf("expected query without type to return true")
	}

	// Unknown type matches all
	if !EvaluateFilter(record, map[string]interface{}{"type": "unknownType"}) {
		t.Errorf("expected unknown type to return true")
	}
}

func TestEvaluateFilter_DimensionExists(t *testing.T) {
	record := map[string]interface{}{
		"command":    "SELECT",
		"dbUserName": "alice",
	}

	// Dimension exists
	queryMatch := map[string]interface{}{
		"type":      "dimensionExists",
		"dimension": "command",
	}
	if !EvaluateFilter(record, queryMatch) {
		t.Errorf("expected dimensionExists to return true for existing dimension")
	}

	// Dimension does not exist
	queryNoMatch := map[string]interface{}{
		"type":      "dimensionExists",
		"dimension": "nonExistent",
	}
	if EvaluateFilter(record, queryNoMatch) {
		t.Errorf("expected dimensionExists to return false for missing dimension")
	}

	// Invalid dimension type (not string)
	queryInvalid := map[string]interface{}{
		"type":      "dimensionExists",
		"dimension": 12345,
	}
	if EvaluateFilter(record, queryInvalid) {
		t.Errorf("expected dimensionExists to return false for non-string dimension")
	}
}

func TestEvaluateFilter_Selector(t *testing.T) {
	record := map[string]interface{}{
		"command":    "SELECT",
		"dbUserName": "alice",
		"rowCount":   float64(10),
	}

	// Match string
	matchQuery := map[string]interface{}{
		"type":      "selector",
		"dimension": "command",
		"value":     "SELECT",
	}
	if !EvaluateFilter(record, matchQuery) {
		t.Errorf("expected selector to match string value")
	}

	// Non-match string
	noMatchQuery := map[string]interface{}{
		"type":      "selector",
		"dimension": "command",
		"value":     "DROP",
	}
	if EvaluateFilter(record, noMatchQuery) {
		t.Errorf("expected selector to not match different value")
	}

	// Non-existent dimension
	missingDimQuery := map[string]interface{}{
		"type":      "selector",
		"dimension": "missingField",
		"value":     "val",
	}
	if EvaluateFilter(record, missingDimQuery) {
		t.Errorf("expected selector to not match missing dimension")
	}

	// Match number
	matchNumQuery := map[string]interface{}{
		"type":      "selector",
		"dimension": "rowCount",
		"value":     float64(10),
	}
	if !EvaluateFilter(record, matchNumQuery) {
		t.Errorf("expected selector to match numeric value")
	}
}

func TestEvaluateFilter_Not(t *testing.T) {
	record := map[string]interface{}{
		"command": "SELECT",
	}

	// NOT match (inner matches -> NOT returns false)
	notMatchQuery := map[string]interface{}{
		"type": "not",
		"field": map[string]interface{}{
			"type":      "selector",
			"dimension": "command",
			"value":     "SELECT",
		},
	}
	if EvaluateFilter(record, notMatchQuery) {
		t.Errorf("expected not(selector=SELECT) to return false when command is SELECT")
	}

	// NOT non-match (inner doesn't match -> NOT returns true)
	notNoMatchQuery := map[string]interface{}{
		"type": "not",
		"field": map[string]interface{}{
			"type":      "selector",
			"dimension": "command",
			"value":     "INSERT",
		},
	}
	if !EvaluateFilter(record, notNoMatchQuery) {
		t.Errorf("expected not(selector=INSERT) to return true when command is SELECT")
	}

	// Malformed field
	notMalformed := map[string]interface{}{
		"type":  "not",
		"field": "invalid-not-map",
	}
	if !EvaluateFilter(record, notMalformed) {
		t.Errorf("expected malformed not to return default true")
	}
}

func TestEvaluateFilter_And(t *testing.T) {
	record := map[string]interface{}{
		"command":    "SELECT",
		"dbUserName": "alice",
	}

	// Both match
	andAllMatch := map[string]interface{}{
		"type": "and",
		"fields": []interface{}{
			map[string]interface{}{"type": "selector", "dimension": "command", "value": "SELECT"},
			map[string]interface{}{"type": "selector", "dimension": "dbUserName", "value": "alice"},
		},
	}
	if !EvaluateFilter(record, andAllMatch) {
		t.Errorf("expected and query to return true when all match")
	}

	// One fails
	andOneFails := map[string]interface{}{
		"type": "and",
		"fields": []interface{}{
			map[string]interface{}{"type": "selector", "dimension": "command", "value": "SELECT"},
			map[string]interface{}{"type": "selector", "dimension": "dbUserName", "value": "bob"},
		},
	}
	if EvaluateFilter(record, andOneFails) {
		t.Errorf("expected and query to return false when one subquery fails")
	}

	// Non-slice fields
	andMalformed := map[string]interface{}{
		"type":   "and",
		"fields": "not-a-slice",
	}
	if !EvaluateFilter(record, andMalformed) {
		t.Errorf("expected malformed and query to return true")
	}
}

func TestEvaluateFilter_Or(t *testing.T) {
	record := map[string]interface{}{
		"command":    "SELECT",
		"dbUserName": "alice",
	}

	// One matches
	orOneMatches := map[string]interface{}{
		"type": "or",
		"fields": []interface{}{
			map[string]interface{}{"type": "selector", "dimension": "command", "value": "DROP"},
			map[string]interface{}{"type": "selector", "dimension": "dbUserName", "value": "alice"},
		},
	}
	if !EvaluateFilter(record, orOneMatches) {
		t.Errorf("expected or query to return true when at least one matches")
	}

	// None match
	orNoneMatch := map[string]interface{}{
		"type": "or",
		"fields": []interface{}{
			map[string]interface{}{"type": "selector", "dimension": "command", "value": "DROP"},
			map[string]interface{}{"type": "selector", "dimension": "dbUserName", "value": "bob"},
		},
	}
	if EvaluateFilter(record, orNoneMatch) {
		t.Errorf("expected or query to return false when none match")
	}

	// Malformed fields
	orMalformed := map[string]interface{}{
		"type":   "or",
		"fields": "invalid",
	}
	if EvaluateFilter(record, orMalformed) {
		t.Errorf("expected malformed or query to return false")
	}
}

func TestEvaluateFilter_Contains(t *testing.T) {
	record := map[string]interface{}{
		"stringField": "hello world from database",
		"sliceField":  []interface{}{"item1", "item2", "item3"},
		"mapField": map[string]interface{}{
			"keyA": "valueA",
			"keyB": "valueB",
		},
		"numField": 42,
	}

	// String substring match
	strContains := map[string]interface{}{
		"type":      "contains",
		"dimension": "stringField",
		"value":     "world",
	}
	if !EvaluateFilter(record, strContains) {
		t.Errorf("expected string contains to match")
	}

	// String substring non-match
	strNoContains := map[string]interface{}{
		"type":      "contains",
		"dimension": "stringField",
		"value":     "notfound",
	}
	if EvaluateFilter(record, strNoContains) {
		t.Errorf("expected string contains to not match")
	}

	// Slice item match
	sliceContains := map[string]interface{}{
		"type":      "contains",
		"dimension": "sliceField",
		"value":     "item2",
	}
	if !EvaluateFilter(record, sliceContains) {
		t.Errorf("expected slice contains to match item")
	}

	// Slice item non-match
	sliceNoContains := map[string]interface{}{
		"type":      "contains",
		"dimension": "sliceField",
		"value":     "item99",
	}
	if EvaluateFilter(record, sliceNoContains) {
		t.Errorf("expected slice contains to not match missing item")
	}

	// Map key match
	mapContains := map[string]interface{}{
		"type":      "contains",
		"dimension": "mapField",
		"value":     "keyA",
	}
	if !EvaluateFilter(record, mapContains) {
		t.Errorf("expected map contains to match key")
	}

	// Map key non-match
	mapNoContains := map[string]interface{}{
		"type":      "contains",
		"dimension": "mapField",
		"value":     "keyZ",
	}
	if EvaluateFilter(record, mapNoContains) {
		t.Errorf("expected map contains to not match missing key")
	}

	// Missing dimension
	missingDim := map[string]interface{}{
		"type":      "contains",
		"dimension": "missingField",
		"value":     "val",
	}
	if EvaluateFilter(record, missingDim) {
		t.Errorf("expected contains to return false for missing dimension")
	}

	// Unsupported type (number)
	unsupportedType := map[string]interface{}{
		"type":      "contains",
		"dimension": "numField",
		"value":     42,
	}
	if EvaluateFilter(record, unsupportedType) {
		t.Errorf("expected contains to return false for unsupported type")
	}
}

func TestLoadFilterConfig(t *testing.T) {
	// Create temp directory for testing filters
	tmpDir := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origWd)
	}()

	err = os.Chdir(tmpDir)
	if err != nil {
		t.Fatalf("failed to chdir to temp dir: %v", err)
	}

	// 1. Missing filter and no default returns empty config
	cfg, err := LoadFilterConfig("nonexistent")
	if err != nil {
		t.Fatalf("expected nil error on missing filter, got %v", err)
	}
	if len(cfg.Drop) != 0 || len(cfg.Query) != 0 {
		t.Errorf("expected empty config on missing filter")
	}

	// 2. Load JSON filter
	filtersDir := filepath.Join(tmpDir, "filters")
	if err := os.MkdirAll(filtersDir, 0755); err != nil {
		t.Fatalf("failed to mkdir filters: %v", err)
	}

	jsonContent := []byte(`{
		"drop": ["paramList", "errorMessage"],
		"query": {"type": "selector", "dimension": "command", "value": "SELECT"}
	}`)
	if err := os.WriteFile(filepath.Join(filtersDir, "custom.json"), jsonContent, 0644); err != nil {
		t.Fatalf("failed to write custom.json: %v", err)
	}

	cfg, err = LoadFilterConfig("custom")
	if err != nil {
		t.Fatalf("expected nil error loading JSON filter, got: %v", err)
	}
	if len(cfg.Drop) != 2 || cfg.Drop[0] != "paramList" {
		t.Errorf("unexpected Drop fields: %+v", cfg.Drop)
	}
	if cfg.Query["type"] != "selector" {
		t.Errorf("unexpected Query type: %v", cfg.Query["type"])
	}

	// 3. Load YAML filter
	yamlContent := []byte(`
drop:
  - clientApplication
query:
  type: dimensionExists
  dimension: objectName
`)
	if err := os.WriteFile(filepath.Join(filtersDir, "audit.yaml"), yamlContent, 0644); err != nil {
		t.Fatalf("failed to write audit.yaml: %v", err)
	}

	cfg, err = LoadFilterConfig("audit")
	if err != nil {
		t.Fatalf("expected nil error loading YAML filter, got: %v", err)
	}
	if len(cfg.Drop) != 1 || cfg.Drop[0] != "clientApplication" {
		t.Errorf("unexpected Drop fields from YAML: %+v", cfg.Drop)
	}

	// 4. Fallback to default.json
	defaultContent := []byte(`{"drop": ["dropFromDefault"], "query": {}}`)
	if err := os.WriteFile(filepath.Join(filtersDir, "default.json"), defaultContent, 0644); err != nil {
		t.Fatalf("failed to write default.json: %v", err)
	}

	cfg, err = LoadFilterConfig("missing-filter-name")
	if err != nil {
		t.Fatalf("expected nil error falling back to default, got: %v", err)
	}
	if len(cfg.Drop) != 1 || cfg.Drop[0] != "dropFromDefault" {
		t.Errorf("expected fallback to default.json, got %+v", cfg.Drop)
	}

	// 5. Malformed file returns error
	if err := os.WriteFile(filepath.Join(filtersDir, "malformed.json"), []byte("{bad json"), 0644); err != nil {
		t.Fatalf("failed to write malformed.json: %v", err)
	}
	_, err = LoadFilterConfig("malformed")
	if err == nil {
		t.Errorf("expected error loading malformed json, got nil")
	}

	// 6. Malformed YAML returns error
	if err := os.WriteFile(filepath.Join(filtersDir, "malformed.yaml"), []byte(":\n  - : bad yaml"), 0644); err != nil {
		t.Fatalf("failed to write malformed.yaml: %v", err)
	}
	_, err = LoadFilterConfig("malformed")
	if err == nil {
		t.Errorf("expected error loading malformed yaml, got nil")
	}
}
