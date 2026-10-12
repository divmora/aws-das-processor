package filter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// OutputConfig defines S3 destination bucket, KMS encryption, path and partition formatting for Parquet files
type OutputConfig struct {
	Bucket        string  `json:"bucket,omitempty" yaml:"bucket,omitempty"`                 // destination S3 bucket name (overrides DAS_OUTPUT_BUCKET)
	KmsKeyID      string  `json:"kms_key_id,omitempty" yaml:"kms_key_id,omitempty"`         // SSE-KMS Key ID/ARN (overrides DAS_OUTPUT_KMS_KEY_ID)
	Format        string  `json:"format,omitempty" yaml:"format,omitempty"`                 // "hive", "date", "mirrored"
	Prefix        *string `json:"prefix,omitempty" yaml:"prefix,omitempty"`                 // e.g. "das" (default if nil) or "" for root
	IncludeFilter bool    `json:"include_filter,omitempty" yaml:"include_filter,omitempty"` // whether to include /<filter-name>/ in path
}

// MatchConfig defines criteria for matching an incoming DAS event by cluster or S3 path
type MatchConfig struct {
	Clusters []string `json:"clusters,omitempty" yaml:"clusters,omitempty"`
	Paths    []string `json:"paths,omitempty" yaml:"paths,omitempty"`
}

// HasCriteria returns true if the match configuration has any cluster or path filters defined.
func (m *MatchConfig) HasCriteria() bool {
	return m != nil && (len(m.Clusters) > 0 || len(m.Paths) > 0)
}

// Matches returns true if the given clusterID and s3Key satisfy the match configuration.
// If both Clusters and Paths are specified, both must match.
// If Clusters is non-empty, clusterID must match at least one pattern.
// If Paths is non-empty, s3Key must match at least one pattern.
func (m *MatchConfig) Matches(clusterID, s3Key string) bool {
	if m == nil {
		return false
	}

	if len(m.Clusters) > 0 {
		matchedCluster := false
		for _, pattern := range m.Clusters {
			if MatchPattern(pattern, clusterID) {
				matchedCluster = true
				break
			}
		}
		if !matchedCluster {
			return false
		}
	}

	if len(m.Paths) > 0 {
		matchedPath := false
		for _, pattern := range m.Paths {
			if MatchPattern(pattern, s3Key) {
				matchedPath = true
				break
			}
		}
		if !matchedPath {
			return false
		}
	}

	return len(m.Clusters) > 0 || len(m.Paths) > 0
}

// MatchPattern checks if target matches pattern, supporting wildcards (* and ?),
// case-insensitive comparisons, and directory prefix matching.
func MatchPattern(pattern, target string) bool {
	if pattern == "*" || pattern == target {
		return true
	}
	if strings.EqualFold(pattern, target) {
		return true
	}
	if strings.HasSuffix(pattern, "/") && strings.HasPrefix(target, pattern) {
		return true
	}

	var sb strings.Builder
	sb.WriteString("^(?i)")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			sb.WriteString(".*")
		case '?':
			sb.WriteString(".")
		case '.', '+', '(', ')', '|', '[', ']', '{', '}', '^', '$', '\\':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteString("$")
	re, err := regexp.Compile(sb.String())
	if err != nil {
		return false
	}
	return re.MatchString(target)
}

// FilterConfig holds the configuration for dropping fields, filtering events, destination output formatting,
// and matching incoming events.
type FilterConfig struct {
	Name      string                 `json:"name,omitempty" yaml:"name,omitempty"`
	Enabled   *bool                  `json:"enabled,omitempty" yaml:"enabled,omitempty"`       // Defaults to true if nil
	KMSRegion string                 `json:"kms_region,omitempty" yaml:"kms_region,omitempty"` // AWS region for source KMS key
	Match     *MatchConfig           `json:"match,omitempty" yaml:"match,omitempty"`
	Drop      []string               `json:"drop" yaml:"drop"`
	Query     map[string]interface{} `json:"query" yaml:"query"`
	Output    *OutputConfig          `json:"output,omitempty" yaml:"output,omitempty"`
}

// IsEnabled returns true if the filter is enabled (default is true if unset).
func (f *FilterConfig) IsEnabled() bool {
	if f == nil {
		return false
	}
	if f.Enabled == nil {
		return true
	}
	return *f.Enabled
}

// LoadFilterFile loads and parses a single filter file (YAML or JSON) from the specified path.
func LoadFilterFile(filePath string) (*FilterConfig, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var config FilterConfig
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == ".yaml" || ext == ".yml" {
		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("failed to unmarshal YAML from %s: %w", filePath, err)
		}
	} else {
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON from %s: %w", filePath, err)
		}
	}

	if config.Name == "" {
		base := filepath.Base(filePath)
		config.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if config.Drop == nil {
		config.Drop = []string{}
	}
	if config.Query == nil {
		config.Query = map[string]interface{}{}
	}

	return &config, nil
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

	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			cfg, err := LoadFilterFile(path)
			if err != nil {
				return nil, err
			}
			return cfg, nil
		}
	}

	return &FilterConfig{
		Name:  filterName,
		Drop:  []string{},
		Query: map[string]interface{}{},
	}, nil
}

// LoadAllFilterConfigs scans a directory for all .yaml, .yml, and .json filter configurations,
// returning them sorted alphabetically by name. If dir is empty, it checks DAS_FILTERS_DIR or defaults to "filters".
func LoadAllFilterConfigs(dir string) ([]*FilterConfig, error) {
	if dir == "" {
		dir = os.Getenv("DAS_FILTERS_DIR")
		if dir == "" {
			dir = "filters"
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*FilterConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read filters directory %q: %w", dir, err)
	}

	var configs []*FilterConfig
	seen := make(map[string]bool)

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			continue
		}

		baseName := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if seen[baseName] {
			continue
		}

		cfg, err := LoadFilterFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to load filter file %q: %w", entry.Name(), err)
		}
		seen[baseName] = true
		configs = append(configs, cfg)
	}

	return configs, nil
}

// ResolveFilters determines which filters should be applied to an incoming event
// given a pool of available filters, the cluster ID, and the S3 key.
// Any disabled filters (enabled: false) are skipped.
func ResolveFilters(configs []*FilterConfig, clusterID, s3Key string) []*FilterConfig {
	var matched []*FilterConfig
	var fallback *FilterConfig

	for _, cfg := range configs {
		if !cfg.IsEnabled() {
			continue
		}
		if cfg.Match.HasCriteria() {
			if cfg.Match.Matches(clusterID, s3Key) {
				matched = append(matched, cfg)
			}
		} else {
			if strings.EqualFold(cfg.Name, "default") {
				fallback = cfg
			} else if fallback == nil {
				fallback = cfg
			}
		}
	}

	if len(matched) > 0 {
		return matched
	}

	if fallback != nil {
		return []*FilterConfig{fallback}
	}

	if len(configs) == 0 {
		if def, err := LoadFilterConfig("default"); err == nil && def != nil && def.IsEnabled() {
			return []*FilterConfig{def}
		}
	}

	return nil
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
