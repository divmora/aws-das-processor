package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FilterConfig holds the configuration for dropping fields and filtering events
type FilterConfig struct {
	Drop  []string               `json:"drop" yaml:"drop"`
	Query map[string]interface{} `json:"query" yaml:"query"`
}

// LoadFilterConfig loads the filter configuration from a JSON or YAML file.
// If the file doesn't exist, it attempts to load default configuration or returns an empty config.
func LoadFilterConfig(filterName string) (*FilterConfig, error) {
	paths := []string{
		filepath.Join("filters", filterName+".yaml"),
		filepath.Join("filters", filterName+".yml"),
		filepath.Join("filters", filterName+".json"),
		filepath.Join("filters", "default.yaml"),
		filepath.Join("filters", "default.yml"),
		filepath.Join("filters", "default.json"),
	}

	var data []byte
	var err error
	var matchedPath string

	for _, path := range paths {
		if _, errStat := os.Stat(path); errStat == nil {
			data, err = os.ReadFile(path)
			if err == nil {
				matchedPath = path
				break
			}
		}
	}

	if matchedPath == "" {
		return &FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}, nil
	}

	var config FilterConfig
	if strings.HasSuffix(matchedPath, ".yaml") || strings.HasSuffix(matchedPath, ".yml") {
		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, err
		}
	} else {
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, err
		}
	}

	return &config, nil
}

// EvaluateFilter parses the recursive AST-like filter query and returns true if the record matches.
func EvaluateFilter(record map[string]interface{}, query map[string]interface{}) bool {
	if len(query) == 0 {
		return true
	}

	typ, ok := query["type"].(string)
	if !ok {
		return true
	}

	switch typ {
	case "not":
		if field, ok := query["field"].(map[string]interface{}); ok {
			return !EvaluateFilter(record, field)
		}
	case "dimensionExists":
		if dim, ok := query["dimension"].(string); ok {
			_, exists := record[dim]
			return exists
		}
		return false
	case "selector":
		dim, dimOk := query["dimension"].(string)
		val, valOk := query["value"]
		if dimOk && valOk {
			return record[dim] == val
		}
		return false
	case "and":
		if fields, ok := query["fields"].([]interface{}); ok {
			for _, f := range fields {
				if fieldQuery, ok := f.(map[string]interface{}); ok {
					if !EvaluateFilter(record, fieldQuery) {
						return false
					}
				}
			}
			return true
		}
	case "or":
		if fields, ok := query["fields"].([]interface{}); ok {
			for _, f := range fields {
				if fieldQuery, ok := f.(map[string]interface{}); ok {
					if EvaluateFilter(record, fieldQuery) {
						return true
					}
				}
			}
		}
		return false
	case "contains":
		dim, dimOk := query["dimension"].(string)
		val, valOk := query["value"]
		if dimOk && valOk {
			if recordVal, exists := record[dim]; exists {
				switch rv := recordVal.(type) {
				case []interface{}:
					for _, item := range rv {
						if item == val {
							return true
						}
					}
				case string:
					if vs, ok := val.(string); ok && strings.Contains(rv, vs) {
						return true
					}
				case map[string]interface{}:
					if vs, ok := val.(string); ok {
						_, has := rv[vs]
						return has
					}
				}
			}
		}
		return false
	}
	return true
}
