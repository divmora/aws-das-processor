package parquet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/divmora/aws-das-processor/pkg/filter"
	"github.com/divmora/aws-das-processor/pkg/model"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/snappy"
)

// MapToDatabaseActivityEvent maps a generic map to model.DatabaseActivityEvent,
// avoiding expensive json.Marshal + json.Unmarshal serialization cycles.
func MapToDatabaseActivityEvent(m map[string]interface{}) model.DatabaseActivityEvent {
	return model.DatabaseActivityEvent{
		LogTime:           GetString(m, "logTime"),
		StatementId:       GetInt64(m, "statementId"),
		SubstatementId:    GetInt64(m, "substatementId"),
		ObjectType:        GetString(m, "objectType"),
		Command:           GetString(m, "command"),
		ObjectName:        GetString(m, "objectName"),
		DatabaseName:      GetString(m, "databaseName"),
		DbUserName:        GetString(m, "dbUserName"),
		RemoteHost:        GetString(m, "remoteHost"),
		SessionId:         GetString(m, "sessionId"),
		RowCount:          GetInt64(m, "rowCount"),
		CommandText:       GetString(m, "commandText"),
		ParamList:         GetStringSlice(m, "paramList"),
		Pid:               GetInt64(m, "pid"),
		ClientApplication: GetString(m, "clientApplication"),
		ExitCode:          GetString(m, "exitCode"),
		Class:             GetString(m, "class"),
		ServerHost:        GetString(m, "serverHost"),
		Type:              GetString(m, "type"),
		StartTime:         GetString(m, "startTime"),
		ErrorMessage:      GetString(m, "errorMessage"),
	}
}

// GetString extracts a string value from map, converting non-strings safely.
func GetString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok && v != nil {
		if s, ok := v.(string); ok {
			return s
		}
		res := fmt.Sprintf("%v", v)
		if res == "<nil>" {
			return ""
		}
		return res
	}
	return ""
}

// GetInt64 extracts an int64 value from a map with numeric type coercions.
func GetInt64(m map[string]interface{}, key string) int64 {
	if v, ok := m[key]; ok && v != nil {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int64:
			return n
		case int:
			return int64(n)
		case json.Number:
			i, err := n.Int64()
			if err != nil {
				slog.Warn("Failed to coerce json.Number to int64", "key", key, "value", n.String(), "error", err)
			}
			return i
		case string:
			i, err := strconv.ParseInt(n, 10, 64)
			if err != nil {
				slog.Warn("Failed to parse string to int64", "key", key, "value", n, "error", err)
			}
			return i
		default:
			slog.Warn("Unexpected numeric type for field", "key", key, "type", fmt.Sprintf("%T", v), "value", fmt.Sprintf("%v", v))
		}
	}
	return 0
}

// GetStringSlice extracts a slice of strings from a map.
func GetStringSlice(m map[string]interface{}, key string) []string {
	if v, ok := m[key]; ok && v != nil {
		switch items := v.(type) {
		case []interface{}:
			result := make([]string, len(items))
			for i, item := range items {
				if s, ok := item.(string); ok {
					result[i] = s
				} else {
					result[i] = fmt.Sprintf("%v", item)
				}
			}
			return result
		case []string:
			return items
		default:
			slog.Warn("Unexpected slice type for field", "key", key, "type", fmt.Sprintf("%T", v), "value", fmt.Sprintf("%v", v))
		}
	}
	return nil
}

// ConvertJSONToParquet transforms decompressed JSON events into Snappy-compressed Parquet bytes,
// applying heartbeat pruning, exit code normalization, field drops, and rule evaluations.
func ConvertJSONToParquet(decompressedJSON []byte, filterConfig *filter.FilterConfig) ([]byte, error) {
	var container struct {
		DatabaseActivityEventList []map[string]interface{} `json:"databaseActivityEventList"`
	}
	if err := json.Unmarshal(decompressedJSON, &container); err != nil {
		slog.Error("Failed to unmarshal JSON events container", "error", err, "payloadBytes", len(decompressedJSON))
		return nil, fmt.Errorf("failed to unmarshal JSON events: %w", err)
	}

	data, _, err := ConvertEventsToParquet(container.DatabaseActivityEventList, filterConfig)
	return data, err
}

// ConvertEventsToParquet transforms a slice of event maps into Snappy-compressed Parquet bytes,
// returning the compressed bytes, the number of matching records written, and any error encountered.
// It ensures that original event maps are not mutated, making it safe for multi-filter evaluation.
func ConvertEventsToParquet(events []map[string]interface{}, filterConfig *filter.FilterConfig) ([]byte, int, error) {
	var buf bytes.Buffer
	writer := parquet.NewWriter(&buf, parquet.SchemaOf(new(model.DatabaseActivityEvent)), parquet.Compression(&snappy.Codec{}))

	totalEvents := len(events)
	heartbeatsPruned := 0
	filteredOut := 0
	rowCount := 0

	for _, rawEvent := range events {
		if rawEvent == nil {
			slog.Warn("Encountered nil event record in events slice, skipping")
			continue
		}

		if t, ok := rawEvent["type"].(string); ok && t == "heartbeat" {
			heartbeatsPruned++
			continue
		}

		eventMap := rawEvent
		if exitCode, ok := eventMap["exitCode"]; ok {
			if _, isStr := exitCode.(string); !isStr {
				eventMap = make(map[string]interface{}, len(rawEvent))
				for k, v := range rawEvent {
					eventMap[k] = v
				}
				eventMap["exitCode"] = fmt.Sprintf("%v", exitCode)
			}
		}

		if filterConfig == nil || filter.EvaluateFilter(eventMap, filterConfig.Query) {
			if filterConfig != nil && len(filterConfig.Drop) > 0 {
				droppedMap := make(map[string]interface{}, len(eventMap))
				for k, v := range eventMap {
					droppedMap[k] = v
				}
				for _, d := range filterConfig.Drop {
					delete(droppedMap, d)
				}
				eventMap = droppedMap
			}

			event := MapToDatabaseActivityEvent(eventMap)
			if err := writer.Write(event); err != nil {
				slog.Error("Failed to write parquet row",
					"error", err,
					"sessionId", event.SessionId,
					"statementId", event.StatementId,
					"dbUserName", event.DbUserName,
					"command", event.Command,
				)
				return nil, 0, fmt.Errorf("failed to write parquet row (session: %s, stmt: %d): %w", event.SessionId, event.StatementId, err)
			}
			rowCount++
		} else {
			filteredOut++
		}
	}

	if err := writer.Close(); err != nil {
		slog.Error("Failed to close parquet writer", "error", err, "rowsAttempted", rowCount)
		return nil, 0, fmt.Errorf("failed to close parquet writer: %w", err)
	}

	slog.Debug("Parquet event conversion completed",
		"totalEvents", totalEvents,
		"writtenRows", rowCount,
		"heartbeatsPruned", heartbeatsPruned,
		"filteredOut", filteredOut,
	)

	return buf.Bytes(), rowCount, nil
}
