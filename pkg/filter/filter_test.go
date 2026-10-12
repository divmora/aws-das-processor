package filter

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

	// 2. Load YAML filter with Output configuration
	filtersDir := filepath.Join(tmpDir, "filters")
	if err := os.MkdirAll(filtersDir, 0755); err != nil {
		t.Fatalf("failed to mkdir filters: %v", err)
	}

	yamlContent := []byte(`
drop:
  - paramList
  - errorMessage
query:
  type: selector
  dimension: command
  value: SELECT
output:
  format: hive
  prefix: analytics
  include_filter: false
`)
	if err := os.WriteFile(filepath.Join(filtersDir, "custom.yaml"), yamlContent, 0644); err != nil {
		t.Fatalf("failed to write custom.yaml: %v", err)
	}

	cfg, err = LoadFilterConfig("custom")
	if err != nil {
		t.Fatalf("expected nil error loading YAML filter, got: %v", err)
	}
	if len(cfg.Drop) != 2 || cfg.Drop[0] != "paramList" {
		t.Errorf("unexpected Drop fields: %+v", cfg.Drop)
	}
	if cfg.Query["type"] != "selector" {
		t.Errorf("unexpected Query type: %v", cfg.Query["type"])
	}
	if cfg.Output == nil || cfg.Output.Format != "hive" || cfg.Output.Prefix == nil || *cfg.Output.Prefix != "analytics" || cfg.Output.IncludeFilter != false {
		t.Errorf("unexpected Output config from YAML: %+v", cfg.Output)
	}

	// 3. Load another YAML filter with date partitioning
	auditContent := []byte(`
drop:
  - clientApplication
query:
  type: dimensionExists
  dimension: objectName
output:
  bucket: custom-dest-bucket
  kms_key_id: custom-kms-key
  format: date
  prefix: data-lake
  include_filter: true
`)
	if err := os.WriteFile(filepath.Join(filtersDir, "audit.yaml"), auditContent, 0644); err != nil {
		t.Fatalf("failed to write audit.yaml: %v", err)
	}

	cfg, err = LoadFilterConfig("audit")
	if err != nil {
		t.Fatalf("expected nil error loading audit YAML filter, got: %v", err)
	}
	if len(cfg.Drop) != 1 || cfg.Drop[0] != "clientApplication" {
		t.Errorf("unexpected Drop fields from YAML: %+v", cfg.Drop)
	}
	if cfg.Output == nil || cfg.Output.Format != "date" || cfg.Output.Prefix == nil || *cfg.Output.Prefix != "data-lake" || cfg.Output.IncludeFilter != true || cfg.Output.Bucket != "custom-dest-bucket" || cfg.Output.KmsKeyID != "custom-kms-key" {
		t.Errorf("unexpected Output config from YAML: %+v", cfg.Output)
	}

	// 4. Fallback to default.yaml
	defaultContent := []byte("drop:\n  - dropFromDefault\nquery: {}\n")
	if err := os.WriteFile(filepath.Join(filtersDir, "default.yaml"), defaultContent, 0644); err != nil {
		t.Fatalf("failed to write default.yaml: %v", err)
	}

	cfg, err = LoadFilterConfig("missing-filter-name")
	if err != nil {
		t.Fatalf("expected nil error falling back to default, got: %v", err)
	}
	if len(cfg.Drop) != 1 || cfg.Drop[0] != "dropFromDefault" {
		t.Errorf("expected fallback to default.yaml, got %+v", cfg.Drop)
	}

	// 5. Non-.yaml files (.json, .yml) are ignored and trigger fallback or empty config
	_ = os.WriteFile(filepath.Join(filtersDir, "ignored.json"), []byte(`{"drop": ["ignored"]}`), 0644)
	_ = os.WriteFile(filepath.Join(filtersDir, "ignored-yml.yml"), []byte("drop:\n  - ignored\n"), 0644)
	cfg, err = LoadFilterConfig("ignored")
	if err != nil {
		t.Fatalf("expected nil error for ignored non-yaml filter, got: %v", err)
	}
	// "ignored" .json was ignored, so it fell back to default.yaml
	if len(cfg.Drop) != 1 || cfg.Drop[0] != "dropFromDefault" {
		t.Errorf("expected fallback to default.yaml when non-yaml filter is requested, got %+v", cfg.Drop)
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

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern string
		target  string
		want    bool
	}{
		{"*", "anything", true},
		{"*", "", true},
		{"exact-match", "exact-match", true},
		{"exact-match", "EXACT-MATCH", true},
		{"cluster-*", "cluster-EXAMPLE1234567890ABCDEF", true},
		{"cluster-*", "db-123", false},
		{"*EXAMPLE*", "cluster-EXAMPLE1234567890ABCDEF", true},
		{"firehose/*", "firehose/cluster-XYZ/2026/10/01/file.json", true},
		{"firehose/", "firehose/cluster-XYZ/2026/10/01/file.json", true},
		{"*/cluster-XYZ/*", "AWSLogs/123/cluster-XYZ/data.json", true},
		{"*.parquet", "data.parquet", true},
		{"*.parquet", "data.json", false},
		{"item-?", "item-1", true},
		{"item-?", "item-12", false},
		{"cluster-[123]", "cluster-[123]", true}, // escaped regex bracket
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.target, func(t *testing.T) {
			got := MatchPattern(tt.pattern, tt.target)
			if got != tt.want {
				t.Errorf("MatchPattern(%q, %q) = %v, want %v", tt.pattern, tt.target, got, tt.want)
			}
		})
	}
}

func TestMatchConfig_Matches(t *testing.T) {
	var nilConfig *MatchConfig
	if nilConfig.Matches("cluster-1", "path/file.json") {
		t.Errorf("expected nil MatchConfig to return false")
	}

	emptyConfig := &MatchConfig{}
	if emptyConfig.Matches("cluster-1", "path/file.json") {
		t.Errorf("expected empty MatchConfig to return false")
	}
	if emptyConfig.HasCriteria() {
		t.Errorf("expected empty MatchConfig to have no criteria")
	}

	clusterOnly := &MatchConfig{
		Clusters: []string{"cluster-PROD*", "cluster-STG"},
	}
	if !clusterOnly.HasCriteria() {
		t.Errorf("expected clusterOnly to have criteria")
	}
	if !clusterOnly.Matches("cluster-PROD-01", "any/path.json") {
		t.Errorf("expected cluster-PROD-01 to match")
	}
	if !clusterOnly.Matches("cluster-STG", "any/path.json") {
		t.Errorf("expected cluster-STG to match")
	}
	if clusterOnly.Matches("cluster-DEV", "any/path.json") {
		t.Errorf("expected cluster-DEV not to match")
	}

	pathOnly := &MatchConfig{
		Paths: []string{"firehose/*", "custom-prefix/*"},
	}
	if !pathOnly.Matches("any-cluster", "firehose/cluster-1/file.json") {
		t.Errorf("expected firehose path to match")
	}
	if pathOnly.Matches("any-cluster", "AWSLogs/cluster-1/file.json") {
		t.Errorf("expected AWSLogs path not to match")
	}

	both := &MatchConfig{
		Clusters: []string{"cluster-PROD"},
		Paths:    []string{"firehose/*"},
	}
	if !both.Matches("cluster-PROD", "firehose/cluster-PROD/file.json") {
		t.Errorf("expected both to match when cluster and path both match")
	}
	if both.Matches("cluster-PROD", "AWSLogs/cluster-PROD/file.json") {
		t.Errorf("expected both not to match when path does not match")
	}
	if both.Matches("cluster-DEV", "firehose/cluster-DEV/file.json") {
		t.Errorf("expected both not to match when cluster does not match")
	}

	bucketOnly := &MatchConfig{
		Buckets: []string{"raw-bucket-prod-*", "secondary-bucket"},
	}
	if !bucketOnly.HasCriteria() {
		t.Errorf("expected bucketOnly to have criteria")
	}
	if !bucketOnly.Matches("any-cluster", "any/path.json", "raw-bucket-prod-us-east-1") {
		t.Errorf("expected raw-bucket-prod-* to match")
	}
	if !bucketOnly.Matches("any-cluster", "any/path.json", "secondary-bucket") {
		t.Errorf("expected secondary-bucket to match")
	}
	if bucketOnly.Matches("any-cluster", "any/path.json", "dev-bucket") {
		t.Errorf("expected dev-bucket not to match")
	}
	if bucketOnly.Matches("any-cluster", "any/path.json") {
		t.Errorf("expected omitting sourceBucket to not match when Buckets criteria is set")
	}

	allThree := &MatchConfig{
		Clusters: []string{"cluster-PROD"},
		Paths:    []string{"firehose/*"},
		Buckets:  []string{"raw-bucket-*"},
	}
	if !allThree.Matches("cluster-PROD", "firehose/cluster-PROD/file.json", "raw-bucket-1") {
		t.Errorf("expected allThree to match when cluster, path, and bucket all match")
	}
	if allThree.Matches("cluster-PROD", "firehose/cluster-PROD/file.json", "other-bucket") {
		t.Errorf("expected allThree not to match when bucket does not match")
	}
}

func TestLoadAllFilterConfigs(t *testing.T) {
	tmpDir := t.TempDir()

	// Empty dir returns empty slice
	configs, err := LoadAllFilterConfigs(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(configs) != 0 {
		t.Errorf("expected 0 configs, got %d", len(configs))
	}

	// Non-existent dir returns empty slice and nil error
	configs, err = LoadAllFilterConfigs(filepath.Join(tmpDir, "does-not-exist"))
	if err != nil || len(configs) != 0 {
		t.Fatalf("expected nil error and 0 configs for non-existent dir, got: %v, len: %d", err, len(configs))
	}

	// Write filter files: three .yaml files, plus ignored .json, .yml, and .txt files
	_ = os.WriteFile(filepath.Join(tmpDir, "b-compliance.yaml"), []byte("name: compliance\ndrop:\n  - mac"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "a-audit.yaml"), []byte("name: audit\ndrop: []\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "c-security.yaml"), []byte("name: security\ndrop: []\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "ignored.json"), []byte(`{"name": "ignored", "drop": []}`), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "ignored.yml"), []byte("name: ignored\ndrop: []\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "readme.txt"), []byte("ignore me"), 0644)

	configs, err = LoadAllFilterConfigs(tmpDir)
	if err != nil {
		t.Fatalf("failed to load configs: %v", err)
	}
	if len(configs) != 3 {
		t.Fatalf("expected exactly 3 .yaml configs (ignoring .json, .yml, .txt), got %d", len(configs))
	}
	// Verify sorting order: a-audit, b-compliance, c-security
	if configs[0].Name != "audit" || configs[1].Name != "compliance" || configs[2].Name != "security" {
		t.Errorf("unexpected loaded order: %s, %s, %s", configs[0].Name, configs[1].Name, configs[2].Name)
	}
}

func TestResolveFilters(t *testing.T) {
	boolFalse := false
	boolTrue := true

	complianceCfg := &FilterConfig{
		Name: "compliance",
		Match: &MatchConfig{
			Clusters: []string{"cluster-PROD*"},
		},
	}
	securityCfg := &FilterConfig{
		Name: "security",
		Match: &MatchConfig{
			Clusters: []string{"cluster-PROD-01"},
			Paths:    []string{"firehose/*"},
		},
	}
	globalCfg := &FilterConfig{
		Name: "global",
		Match: &MatchConfig{
			Clusters: []string{"*"},
		},
	}
	disabledCfg := &FilterConfig{
		Name:    "disabled-audit",
		Enabled: &boolFalse,
		Match: &MatchConfig{
			Clusters: []string{"*"},
		},
	}
	defaultCfg := &FilterConfig{
		Name:    "default",
		Enabled: &boolTrue,
		Match:   nil, // fallback
	}

	configs := []*FilterConfig{complianceCfg, securityCfg, globalCfg, disabledCfg, defaultCfg}

	// 1. Multiple matching filters (disabled filter must be ignored)
	// For cluster-PROD-01 and firehose path: compliance, security, and global all match!
	res := ResolveFilters(configs, "cluster-PROD-01", "firehose/cluster-PROD-01/data.json")
	if len(res) != 3 {
		t.Fatalf("expected 3 filters to match, got %d", len(res))
	}
	for _, f := range res {
		if f.Name == "disabled-audit" {
			t.Errorf("disabled filter should not be matched")
		}
	}

	// 2. Single match
	// For cluster-DEV and custom path: only global matches!
	res = ResolveFilters(configs, "cluster-DEV", "custom/data.json")
	if len(res) != 1 || res[0].Name != "global" {
		t.Fatalf("expected only global filter to match, got %+v", res)
	}

	// 3. Fallback to default when no filters with criteria match
	noGlobalConfigs := []*FilterConfig{complianceCfg, securityCfg, defaultCfg}
	res = ResolveFilters(noGlobalConfigs, "cluster-UNKNOWN", "custom/data.json")
	if len(res) != 1 || res[0].Name != "default" {
		t.Fatalf("expected fallback to default filter, got %+v", res)
	}

	// 4. No matches and no fallback
	onlyProdConfigs := []*FilterConfig{complianceCfg}
	res = ResolveFilters(onlyProdConfigs, "cluster-DEV", "custom/data.json")
	if len(res) != 0 {
		t.Errorf("expected 0 matches when no filter matches and no default exists, got %d", len(res))
	}

	// 5. Bucket-specific match
	bucketFilterA := &FilterConfig{
		Name: "bucket-a-filter",
		Match: &MatchConfig{
			Buckets: []string{"source-bucket-a"},
		},
	}
	bucketFilterB := &FilterConfig{
		Name: "bucket-b-filter",
		Match: &MatchConfig{
			Buckets: []string{"source-bucket-b"},
		},
	}
	bucketConfigs := []*FilterConfig{bucketFilterA, bucketFilterB}

	resA := ResolveFilters(bucketConfigs, "cluster-1", "path/data.json", "source-bucket-a")
	if len(resA) != 1 || resA[0].Name != "bucket-a-filter" {
		t.Fatalf("expected bucket-a-filter to match source-bucket-a, got %+v", resA)
	}

	resB := ResolveFilters(bucketConfigs, "cluster-1", "path/data.json", "source-bucket-b")
	if len(resB) != 1 || resB[0].Name != "bucket-b-filter" {
		t.Fatalf("expected bucket-b-filter to match source-bucket-b, got %+v", resB)
	}

	resC := ResolveFilters(bucketConfigs, "cluster-1", "path/data.json", "source-bucket-other")
	if len(resC) != 0 {
		t.Fatalf("expected 0 matches for non-matching bucket, got %d", len(resC))
	}
}

func TestResolveFiltersDir(t *testing.T) {
	origFiltersDir := os.Getenv("DAS_FILTERS_DIR")
	origTaskRoot := os.Getenv("LAMBDA_TASK_ROOT")
	defer func() {
		if origFiltersDir != "" {
			_ = os.Setenv("DAS_FILTERS_DIR", origFiltersDir)
		} else {
			_ = os.Unsetenv("DAS_FILTERS_DIR")
		}
		if origTaskRoot != "" {
			_ = os.Setenv("LAMBDA_TASK_ROOT", origTaskRoot)
		} else {
			_ = os.Unsetenv("LAMBDA_TASK_ROOT")
		}
	}()

	// Case 1: DAS_FILTERS_DIR takes highest precedence even if LAMBDA_TASK_ROOT is set
	_ = os.Setenv("DAS_FILTERS_DIR", "/custom/filters/path")
	_ = os.Setenv("LAMBDA_TASK_ROOT", "/var/task")
	if got := ResolveFiltersDir(); got != "/custom/filters/path" {
		t.Errorf("expected DAS_FILTERS_DIR to override LAMBDA_TASK_ROOT, got %q", got)
	}

	// Case 2: Only DAS_FILTERS_DIR is set
	_ = os.Setenv("DAS_FILTERS_DIR", "/another/custom/dir")
	_ = os.Unsetenv("LAMBDA_TASK_ROOT")
	if got := ResolveFiltersDir(); got != "/another/custom/dir" {
		t.Errorf("expected /another/custom/dir, got %q", got)
	}

	// Case 3: DAS_FILTERS_DIR unset, LAMBDA_TASK_ROOT set (Lambda runtime)
	_ = os.Unsetenv("DAS_FILTERS_DIR")
	_ = os.Setenv("LAMBDA_TASK_ROOT", "/var/task")
	expectedLambda := filepath.Join("/var/task", "filters")
	if got := ResolveFiltersDir(); got != expectedLambda {
		t.Errorf("expected %q, got %q", expectedLambda, got)
	}

	// Case 4: Neither is set (local fallback)
	_ = os.Unsetenv("DAS_FILTERS_DIR")
	_ = os.Unsetenv("LAMBDA_TASK_ROOT")
	if got := ResolveFiltersDir(); got != "filters" {
		t.Errorf("expected \"filters\", got %q", got)
	}
}

func TestLoadFilterConfigFromDir(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Neither requested filter nor default exists
	cfg, err := LoadFilterConfigFromDir(tmpDir, "missing-filter")
	if err != nil {
		t.Fatalf("expected nil error on missing filter, got %v", err)
	}
	if len(cfg.Drop) != 0 || len(cfg.Query) != 0 {
		t.Errorf("expected empty config on missing filter")
	}

	// 2. Default exists, requested filter missing -> fallback to default
	defaultContent := []byte("name: default\ndrop:\n  - fallbackDrop\nquery: {}\n")
	if err := os.WriteFile(filepath.Join(tmpDir, "default.yaml"), defaultContent, 0644); err != nil {
		t.Fatalf("failed to write default.yaml: %v", err)
	}

	cfg, err = LoadFilterConfigFromDir(tmpDir, "nonexistent-specific")
	if err != nil {
		t.Fatalf("expected nil error on fallback to default, got %v", err)
	}
	if len(cfg.Drop) != 1 || cfg.Drop[0] != "fallbackDrop" {
		t.Errorf("expected fallback to default config, got %+v", cfg.Drop)
	}

	// 3. Exact filter exists -> loads requested filter
	customContent := []byte("name: custom\ndrop:\n  - customDrop\nquery: {}\n")
	if err := os.WriteFile(filepath.Join(tmpDir, "custom.yaml"), customContent, 0644); err != nil {
		t.Fatalf("failed to write custom.yaml: %v", err)
	}
	cfg, err = LoadFilterConfigFromDir(tmpDir, "custom")
	if err != nil {
		t.Fatalf("expected nil error loading custom filter, got %v", err)
	}
	if len(cfg.Drop) != 1 || cfg.Drop[0] != "customDrop" {
		t.Errorf("expected custom config, got %+v", cfg.Drop)
	}
}
