package parquet

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/divmora/aws-das-processor/pkg/filter"
	"github.com/divmora/aws-das-processor/pkg/model"
	"github.com/parquet-go/parquet-go"
)

func findTestDataFile(t testing.TB, filename string) []byte {
	paths := []string{
		filepath.Join("testdata", filename),
		filepath.Join("..", "..", "testdata", filename),
	}
	for _, p := range paths {
		if data, err := os.ReadFile(p); err == nil {
			return data
		}
	}
	t.Fatalf("could not find testdata file %q in expected paths", filename)
	return nil
}

// Scenario 1: Heartbeat filtering — heartbeat events should be skipped
func TestConvertJSONToParquet_HeartbeatFiltering(t *testing.T) {
	rawJSON := []byte(`{
		"databaseActivityEventList": [
			{"type": "heartbeat"},
			{"type": "heartbeat"},
			{"type": "activity", "command": "SELECT", "dbUserName": "alice"}
		]
	}`)

	filterConfig := &filter.FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}
	parquetBytes, err := ConvertJSONToParquet(rawJSON, filterConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reader := parquet.NewReader(bytes.NewReader(parquetBytes))
	var rows []model.DatabaseActivityEvent
	for {
		var row model.DatabaseActivityEvent
		err := reader.Read(&row)
		if err != nil {
			break
		}
		rows = append(rows, row)
	}

	if len(rows) != 1 {
		t.Fatalf("expected 1 row (heartbeats excluded), got %d", len(rows))
	}
	if rows[0].DbUserName != "alice" {
		t.Errorf("expected alice, got %s", rows[0].DbUserName)
	}
}

// Scenario 2: Drop fields — configured drop fields should not appear in output
func TestConvertJSONToParquet_DropFields(t *testing.T) {
	rawJSON := []byte(`{
		"databaseActivityEventList": [
			{"type": "activity", "command": "SELECT", "dbUserName": "alice", "errorMessage": "sensitive-err"}
		]
	}`)

	filterConfig := &filter.FilterConfig{
		Drop:  []string{"errorMessage", "command"},
		Query: map[string]interface{}{},
	}
	parquetBytes, err := ConvertJSONToParquet(rawJSON, filterConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reader := parquet.NewReader(bytes.NewReader(parquetBytes))
	var row model.DatabaseActivityEvent
	err = reader.Read(&row)
	if err != nil {
		t.Fatalf("failed to read row: %v", err)
	}

	if row.ErrorMessage != "" {
		t.Errorf("expected errorMessage to be dropped, got %q", row.ErrorMessage)
	}
	if row.Command != "" {
		t.Errorf("expected command to be dropped, got %q", row.Command)
	}
	if row.DbUserName != "alice" {
		t.Errorf("expected dbUserName to be alice, got %q", row.DbUserName)
	}
}

// Scenario 3: Filter query matching — only events matching query are preserved
func TestConvertJSONToParquet_FilterQueryMatching(t *testing.T) {
	rawJSON := []byte(`{
		"databaseActivityEventList": [
			{"type": "activity", "command": "SELECT", "dbUserName": "alice"},
			{"type": "activity", "command": "DROP", "dbUserName": "malory"},
			{"type": "activity", "command": "SELECT", "dbUserName": "bob"}
		]
	}`)

	filterConfig := &filter.FilterConfig{
		Drop: []string{},
		Query: map[string]interface{}{
			"type":      "selector",
			"dimension": "command",
			"value":     "DROP",
		},
	}
	parquetBytes, err := ConvertJSONToParquet(rawJSON, filterConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reader := parquet.NewReader(bytes.NewReader(parquetBytes))
	var rows []model.DatabaseActivityEvent
	for {
		var row model.DatabaseActivityEvent
		err := reader.Read(&row)
		if err != nil {
			break
		}
		rows = append(rows, row)
	}

	if len(rows) != 1 {
		t.Fatalf("expected 1 row matching DROP, got %d", len(rows))
	}
	if rows[0].DbUserName != "malory" {
		t.Errorf("expected malory, got %s", rows[0].DbUserName)
	}
}

// Scenario 4: Malformed JSON — returns error
func TestConvertJSONToParquet_MalformedJSON(t *testing.T) {
	filterConfig := &filter.FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}
	_, err := ConvertJSONToParquet([]byte("{not-valid-json"), filterConfig)
	if err == nil {
		t.Fatal("expected error on malformed JSON, got nil")
	}
}

// Scenario 5: Empty event list — returns valid parquet with 0 rows
func TestConvertJSONToParquet_EmptyEventList(t *testing.T) {
	rawJSON := []byte(`{"databaseActivityEventList": []}`)
	filterConfig := &filter.FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}

	parquetBytes, err := ConvertJSONToParquet(rawJSON, filterConfig)
	if err != nil {
		t.Fatalf("unexpected error on empty event list: %v", err)
	}

	reader := parquet.NewReader(bytes.NewReader(parquetBytes))
	var rows []model.DatabaseActivityEvent
	for {
		var row model.DatabaseActivityEvent
		if err := reader.Read(&row); err != nil {
			break
		}
		rows = append(rows, row)
	}

	if len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
}

// Scenario 6: ExitCode formatting — numeric exit codes are converted to string
func TestConvertJSONToParquet_ExitCodeConversion(t *testing.T) {
	rawJSON := []byte(`{
		"databaseActivityEventList": [
			{"type": "activity", "exitCode": 0, "dbUserName": "alice"},
			{"type": "activity", "exitCode": "SUCCESS", "dbUserName": "bob"}
		]
	}`)

	filterConfig := &filter.FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}
	parquetBytes, err := ConvertJSONToParquet(rawJSON, filterConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reader := parquet.NewReader(bytes.NewReader(parquetBytes))
	var rows []model.DatabaseActivityEvent
	for {
		var row model.DatabaseActivityEvent
		if err := reader.Read(&row); err != nil {
			break
		}
		rows = append(rows, row)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].ExitCode != "0" {
		t.Errorf("expected '0' exit code, got %q", rows[0].ExitCode)
	}
	if rows[1].ExitCode != "SUCCESS" {
		t.Errorf("expected 'SUCCESS' exit code, got %q", rows[1].ExitCode)
	}
}

func TestFixtures_Unmarshal(t *testing.T) {
	s3Data := findTestDataFile(t, "s3_event_notification.json")
	var s3Notif model.S3EventNotification
	if err := json.Unmarshal(s3Data, &s3Notif); err != nil {
		t.Fatalf("failed to unmarshal s3 fixture: %v", err)
	}
	if len(s3Notif.Records) != 1 || s3Notif.Records[0].S3.Bucket.Name != "aws-das-source-bucket" {
		t.Errorf("unexpected s3 fixture content: %+v", s3Notif)
	}

	// DAS events fixture
	dasData := findTestDataFile(t, "das_events.json")
	filterCfg := &filter.FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}
	parquetBytes, err := ConvertJSONToParquet(dasData, filterCfg)
	if err != nil {
		t.Fatalf("failed to convert das_events fixture to parquet: %v", err)
	}
	if len(parquetBytes) == 0 {
		t.Errorf("expected non-empty parquet output for das_events fixture")
	}

	// Heartbeat-only fixture
	hbData := findTestDataFile(t, "das_heartbeat_only.json")
	hbBytes, err := ConvertJSONToParquet(hbData, filterCfg)
	if err != nil {
		t.Fatalf("failed to convert heartbeat fixture: %v", err)
	}
	if len(hbBytes) == 0 {
		t.Errorf("expected non-empty parquet schema header for heartbeat fixture")
	}

	// Empty fixture
	emptyData := findTestDataFile(t, "das_empty.json")
	emptyBytes, err := ConvertJSONToParquet(emptyData, filterCfg)
	if err != nil {
		t.Fatalf("failed to convert empty fixture: %v", err)
	}
	if len(emptyBytes) == 0 {
		t.Errorf("expected non-empty parquet schema header for empty fixture")
	}
}

func TestMapToDatabaseActivityEvent_AllFields(t *testing.T) {
	input := map[string]interface{}{
		"logTime":           "2026-09-29 12:00:00.000",
		"statementId":       float64(1001),
		"substatementId":    float64(2),
		"objectType":        "TABLE",
		"command":           "SELECT",
		"objectName":        "users",
		"databaseName":      "prod_db",
		"dbUserName":        "app_user",
		"remoteHost":        "10.0.1.45",
		"sessionId":         "sess-998877",
		"rowCount":          float64(42),
		"commandText":       "SELECT 1",
		"paramList":         []interface{}{"val1", "val2", 123},
		"pid":               float64(9876),
		"clientApplication": "service-app",
		"exitCode":          0,
		"class":             "READ",
		"serverHost":        "ip-10-0-2-10",
		"type":              "activity",
		"startTime":         "2026-09-29 12:00:00.000",
		"errorMessage":      "none",
	}

	event := MapToDatabaseActivityEvent(input)

	if event.LogTime != "2026-09-29 12:00:00.000" {
		t.Errorf("unexpected LogTime: %s", event.LogTime)
	}
	if event.StatementId != 1001 {
		t.Errorf("unexpected StatementId: %d", event.StatementId)
	}
	if event.SubstatementId != 2 {
		t.Errorf("unexpected SubstatementId: %d", event.SubstatementId)
	}
	if event.ObjectType != "TABLE" || event.Command != "SELECT" || event.ObjectName != "users" {
		t.Errorf("unexpected object/command fields: %+v", event)
	}
	if event.DatabaseName != "prod_db" || event.DbUserName != "app_user" || event.RemoteHost != "10.0.1.45" {
		t.Errorf("unexpected database connection fields: %+v", event)
	}
	if event.SessionId != "sess-998877" || event.RowCount != 42 || event.CommandText != "SELECT 1" {
		t.Errorf("unexpected session/row fields: %+v", event)
	}
	if len(event.ParamList) != 3 || event.ParamList[0] != "val1" || event.ParamList[2] != "123" {
		t.Errorf("unexpected ParamList: %+v", event.ParamList)
	}
	if event.Pid != 9876 || event.ClientApplication != "service-app" {
		t.Errorf("unexpected pid/clientApp: %+v", event)
	}
	if event.ExitCode != "0" {
		t.Errorf("unexpected ExitCode (expected string '0'): %q", event.ExitCode)
	}
	if event.Class != "READ" || event.ServerHost != "ip-10-0-2-10" || event.Type != "activity" {
		t.Errorf("unexpected class/serverHost/type: %+v", event)
	}
	if event.StartTime != "2026-09-29 12:00:00.000" || event.ErrorMessage != "none" {
		t.Errorf("unexpected startTime/errorMessage: %+v", event)
	}
}

func TestGetHelpers_TypeConversions(t *testing.T) {
	m := map[string]interface{}{
		"str":       "hello",
		"numStr":    12345,
		"int64Val":  int64(999),
		"intVal":    int(888),
		"strNum":    "777",
		"badNum":    "not-a-number",
		"nilVal":    nil,
		"sliceStr":  []string{"a", "b"},
		"sliceAny":  []interface{}{"c", 4},
		"notASlice": "scalar",
	}

	// getString
	if GetString(m, "str") != "hello" {
		t.Errorf("expected 'hello', got %s", GetString(m, "str"))
	}
	if GetString(m, "numStr") != "12345" {
		t.Errorf("expected '12345', got %s", GetString(m, "numStr"))
	}
	if GetString(m, "missing") != "" {
		t.Errorf("expected empty string for missing key")
	}
	if GetString(m, "nilVal") != "" {
		t.Errorf("expected empty string for nil value")
	}

	// getInt64
	if GetInt64(m, "int64Val") != 999 {
		t.Errorf("expected 999, got %d", GetInt64(m, "int64Val"))
	}
	if GetInt64(m, "intVal") != 888 {
		t.Errorf("expected 888, got %d", GetInt64(m, "intVal"))
	}
	if GetInt64(m, "strNum") != 777 {
		t.Errorf("expected 777, got %d", GetInt64(m, "strNum"))
	}
	if GetInt64(m, "badNum") != 0 {
		t.Errorf("expected 0 for bad number string, got %d", GetInt64(m, "badNum"))
	}
	if GetInt64(m, "missing") != 0 {
		t.Errorf("expected 0 for missing key")
	}

	// getStringSlice
	s1 := GetStringSlice(m, "sliceStr")
	if len(s1) != 2 || s1[0] != "a" {
		t.Errorf("unexpected string slice: %+v", s1)
	}
	s2 := GetStringSlice(m, "sliceAny")
	if len(s2) != 2 || s2[1] != "4" {
		t.Errorf("unexpected any slice: %+v", s2)
	}
	if GetStringSlice(m, "notASlice") != nil {
		t.Errorf("expected nil for non-slice")
	}
	if GetStringSlice(m, "missing") != nil {
		t.Errorf("expected nil for missing key")
	}
}

func TestConvertEventsToParquet_MultiFilterIsolation(t *testing.T) {
	rawEvents := []map[string]interface{}{
		{
			"command":      "SELECT 1",
			"dbUserName":   "alice",
			"errorMessage": "sensitive-error-text",
			"exitCode":     0,
		},
	}

	filter1 := &filter.FilterConfig{
		Drop:  []string{"errorMessage"},
		Query: map[string]interface{}{},
	}
	filter2 := &filter.FilterConfig{
		Drop:  []string{},
		Query: map[string]interface{}{},
	}

	// 1. First filter drops errorMessage
	b1, count1, err := ConvertEventsToParquet(rawEvents, filter1)
	if err != nil || count1 != 1 {
		t.Fatalf("filter1 failed: %v, count=%d", err, count1)
	}

	reader1 := parquet.NewReader(bytes.NewReader(b1))
	var row1 model.DatabaseActivityEvent
	if err := reader1.Read(&row1); err != nil {
		t.Fatalf("failed to read row1: %v", err)
	}
	if row1.ErrorMessage != "" {
		t.Errorf("expected row1.ErrorMessage to be dropped, got %q", row1.ErrorMessage)
	}

	// 2. Second filter running on the same rawEvents must still see errorMessage
	b2, count2, err := ConvertEventsToParquet(rawEvents, filter2)
	if err != nil || count2 != 1 {
		t.Fatalf("filter2 failed: %v, count=%d", err, count2)
	}

	reader2 := parquet.NewReader(bytes.NewReader(b2))
	var row2 model.DatabaseActivityEvent
	if err := reader2.Read(&row2); err != nil {
		t.Fatalf("failed to read row2: %v", err)
	}
	if row2.ErrorMessage != "sensitive-error-text" {
		t.Errorf("expected row2.ErrorMessage to be preserved, got %q", row2.ErrorMessage)
	}

	// 3. Raw events map itself must not have lost errorMessage
	if rawEvents[0]["errorMessage"] != "sensitive-error-text" {
		t.Errorf("raw event map was unexpectedly mutated in place: %+v", rawEvents[0])
	}
}

func BenchmarkConvertJSONToParquet(b *testing.B) {
	dasData := findTestDataFile(b, "das_events.json")
	filterCfg := &filter.FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := ConvertJSONToParquet(dasData, filterCfg)
		if err != nil {
			b.Fatalf("convertJSONToParquet error: %v", err)
		}
	}
}
