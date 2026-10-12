package processor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/divmora/aws-das-processor/internal/crypto"
	"github.com/divmora/aws-das-processor/internal/parquet"
	"github.com/divmora/aws-das-processor/pkg/filter"
	"github.com/divmora/aws-das-processor/pkg/model"
)

// S3Client defines the interface for S3 operations used by the processor.
type S3Client interface {
	GetObject(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	PutObject(ctx context.Context, in *s3.PutObjectInput, opt ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// KMSClient defines the interface for KMS operations used by the processor.
type KMSClient interface {
	Decrypt(ctx context.Context, in *kms.DecryptInput, opt ...func(*kms.Options)) (*kms.DecryptOutput, error)
}

// Processor coordinates SQS polling, envelope decryption, filtering, and Parquet sinking.
type Processor struct {
	S3Client      S3Client
	KMSClient     KMSClient
	FilterConfigs []*filter.FilterConfig
	FiltersDir    string
}

// New creates a new Processor instance with the provided AWS clients.
func New(s3Client S3Client, kmsClient KMSClient) *Processor {
	filtersDir := os.Getenv("DAS_FILTERS_DIR")
	if filtersDir == "" {
		filtersDir = "filters"
	}
	return &Processor{
		S3Client:   s3Client,
		KMSClient:  kmsClient,
		FiltersDir: filtersDir,
	}
}

// SetFilterConfigs configures preloaded filter configurations on the processor.
func (p *Processor) SetFilterConfigs(configs []*filter.FilterConfig) {
	p.FilterConfigs = configs
}

// ResolveFiltersForEvent determines which filters match the given event, using custom overrides,
// preloaded configs, or auto-discovering from FiltersDir, and taking into account cluster, S3 key, and optional source bucket.
func (p *Processor) ResolveFiltersForEvent(clusterID, s3Key string, overrideFilters []*filter.FilterConfig, sourceBucket ...string) []*filter.FilterConfig {
	configs := overrideFilters
	if len(configs) == 0 {
		configs = p.FilterConfigs
	}
	if len(configs) == 0 && p.FiltersDir != "" {
		if loaded, err := filter.LoadAllFilterConfigs(p.FiltersDir); err == nil && len(loaded) > 0 {
			configs = loaded
			p.FilterConfigs = loaded
		}
	}
	return filter.ResolveFilters(configs, clusterID, s3Key, sourceBucket...)
}

// ProcessSQSEvent processes each SQS message using partial batch failure tracking.
func (p *Processor) ProcessSQSEvent(ctx context.Context, sqsEvent events.SQSEvent, rdsResourceID string, customFilters ...*filter.FilterConfig) (events.SQSEventResponse, error) {
	var response events.SQSEventResponse

	for _, message := range sqsEvent.Records {
		slog.Info("Processing SQS message", "MessageId", message.MessageId)

		if err := p.ProcessMessage(ctx, message, rdsResourceID, customFilters...); err != nil {
			slog.Error("Failed to process SQS message", "MessageId", message.MessageId, "error", err)
			response.BatchItemFailures = append(response.BatchItemFailures, events.SQSBatchItemFailure{
				ItemIdentifier: message.MessageId,
			})
		}
	}

	return response, nil
}

// S3RecordRef contains the target S3 bucket and object key extracted from an event notification envelope.
type S3RecordRef struct {
	Bucket string
	Key    string
}

// IsS3TestEvent returns true if the message is an S3 test notification (s3:TestEvent),
// either sent directly or wrapped inside an Amazon SNS notification envelope.
func IsS3TestEvent(body string) bool {
	var directTest struct {
		Event string `json:"Event"`
	}
	if err := json.Unmarshal([]byte(body), &directTest); err == nil && directTest.Event == "s3:TestEvent" {
		return true
	}

	var snsMsg struct {
		Type    string `json:"Type"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal([]byte(body), &snsMsg); err == nil && snsMsg.Type == "Notification" && snsMsg.Message != "" {
		if err := json.Unmarshal([]byte(snsMsg.Message), &directTest); err == nil && directTest.Event == "s3:TestEvent" {
			return true
		}
	}

	return false
}

// ParseS3Records extracts S3 bucket and object key references from multiple event notification envelopes:
// 1. Amazon EventBridge S3 notification (modern default): {"source":"aws.s3","detail":{"bucket":{"name":"..."},"object":{"key":"..."}}}
// 2. Amazon SNS wrapped S3 notification: {"Type":"Notification","Message":"..."}
// 3. Direct Amazon S3 bucket notification: {"Records":[{"s3":{"bucket":{"name":"..."},"object":{"key":"..."}}}]}
// 4. Amazon S3 test notification (s3:TestEvent): returns empty slice and nil error
func ParseS3Records(body string) ([]S3RecordRef, error) {
	// 0. Check for Amazon S3 test event (s3:TestEvent)
	if IsS3TestEvent(body) {
		slog.Info("Received S3 test event notification, skipping")
		return []S3RecordRef{}, nil
	}

	// 1. Check for Amazon EventBridge notification (modern standard)
	var ebEvent model.S3EventBridgeNotification
	if err := json.Unmarshal([]byte(body), &ebEvent); err == nil && ebEvent.Source == "aws.s3" && ebEvent.Detail.Bucket.Name != "" && ebEvent.Detail.Object.Key != "" {
		return []S3RecordRef{{
			Bucket: ebEvent.Detail.Bucket.Name,
			Key:    ebEvent.Detail.Object.Key,
		}}, nil
	}

	// 2. Check for Amazon SNS wrapped notification
	var snsMsg struct {
		Type    string `json:"Type"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal([]byte(body), &snsMsg); err == nil && snsMsg.Type == "Notification" && snsMsg.Message != "" {
		// Inside SNS Message could be standard S3 event or EventBridge S3 event
		var innerS3 model.S3EventNotification
		if err := json.Unmarshal([]byte(snsMsg.Message), &innerS3); err == nil && len(innerS3.Records) > 0 {
			var targets []S3RecordRef
			for _, r := range innerS3.Records {
				if r.S3.Bucket.Name != "" && r.S3.Object.Key != "" {
					targets = append(targets, S3RecordRef{Bucket: r.S3.Bucket.Name, Key: r.S3.Object.Key})
				}
			}
			if len(targets) > 0 {
				return targets, nil
			}
		}

		var innerEB model.S3EventBridgeNotification
		if err := json.Unmarshal([]byte(snsMsg.Message), &innerEB); err == nil && innerEB.Detail.Bucket.Name != "" && innerEB.Detail.Object.Key != "" {
			return []S3RecordRef{{
				Bucket: innerEB.Detail.Bucket.Name,
				Key:    innerEB.Detail.Object.Key,
			}}, nil
		}

		return nil, fmt.Errorf("failed to parse S3 event from SNS message payload")
	}

	// 3. Check for direct S3 bucket notification
	var s3Event model.S3EventNotification
	if err := json.Unmarshal([]byte(body), &s3Event); err == nil && len(s3Event.Records) > 0 {
		var targets []S3RecordRef
		for _, r := range s3Event.Records {
			if r.S3.Bucket.Name != "" && r.S3.Object.Key != "" {
				targets = append(targets, S3RecordRef{Bucket: r.S3.Bucket.Name, Key: r.S3.Object.Key})
			}
		}
		if len(targets) > 0 {
			return targets, nil
		}
	}

	return nil, fmt.Errorf("failed to parse S3 event: unrecognized message envelope format")
}

// ProcessMessage handles the complete pipeline for a single SQS message, applying all matched filters
// to the decrypted and decompressed payload in memory.
func (p *Processor) ProcessMessage(ctx context.Context, message events.SQSMessage, rdsResourceID string, customFilters ...*filter.FilterConfig) error {
	records, err := ParseS3Records(message.Body)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}

	for _, record := range records {
		sourceBucket := record.Bucket
		key := record.Key

		if IsIgnoredS3Key(key) {
			slog.Info("Skipping Firehose error or non-data S3 object",
				"SourceBucket", sourceBucket,
				"Key", key,
			)
			continue
		}

		clusterID := ExtractRDSResourceID(key, rdsResourceID)
		if clusterID == "" {
			return fmt.Errorf("missing RDS resource ID: unable to extract from S3 key %q and no fallback DAS_RDS_RESOURCE_ID configured", key)
		}

		matchedFilters := p.ResolveFiltersForEvent(clusterID, key, customFilters, sourceBucket)
		if len(matchedFilters) == 0 {
			slog.Info("No matching filters found for event, skipping",
				"SourceBucket", sourceBucket,
				"Key", key,
				"ResourceID", clusterID,
			)
			continue
		}

		slog.Info("Fetching S3 object",
			"SourceBucket", sourceBucket,
			"Key", key,
			"ResourceID", clusterID,
			"MatchedFiltersCount", len(matchedFilters),
		)

		// 1. Fetch raw object from S3
		getObjectOutput, err := p.S3Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(sourceBucket),
			Key:    aws.String(key),
		})
		if err != nil {
			return fmt.Errorf("failed to fetch object %s/%s: %w", sourceBucket, key, err)
		}

		bodyBytes, err := io.ReadAll(getObjectOutput.Body)
		getObjectOutput.Body.Close()
		if err != nil {
			return fmt.Errorf("failed to read S3 object body: %w", err)
		}

		// 2. Parse the DAS Payload
		var payload model.DASPayload
		if err := json.Unmarshal(bodyBytes, &payload); err != nil {
			return fmt.Errorf("failed to parse DAS payload: %w", err)
		}

		// 3. Decrypt KMS data key
		decodedKmsKey, err := base64.StdEncoding.DecodeString(payload.Key)
		if err != nil {
			return fmt.Errorf("failed to base64 decode KMS key: %w", err)
		}

		kmsRegion := ResolveKMSRegion(matchedFilters)
		slog.Info("Decrypting KMS data key",
			"ResourceID", clusterID,
			"KMSRegion", kmsRegion,
		)

		decryptOutput, err := p.KMSClient.Decrypt(ctx, &kms.DecryptInput{
			CiphertextBlob: decodedKmsKey,
			EncryptionContext: map[string]string{
				"aws:rds:dbc-id": clusterID,
			},
		}, func(o *kms.Options) {
			if kmsRegion != "" {
				o.Region = kmsRegion
			}
		})
		if err != nil {
			return fmt.Errorf("failed to decrypt KMS data key: %w", err)
		}

		plaintextDataKey := decryptOutput.Plaintext

		// 4. Decrypt events payload using AWS Encryption SDK
		decodedPayload, err := base64.StdEncoding.DecodeString(payload.DatabaseActivityEvents)
		if err != nil {
			return fmt.Errorf("failed to base64 decode events payload: %w", err)
		}

		decryptedPayload, err := crypto.DecryptAWSEncryptionSDKPayload(ctx, decodedPayload, plaintextDataKey)
		if err != nil {
			return fmt.Errorf("failed to decrypt events payload: %w", err)
		}

		// 5. Decompress the payload
		decompressedPayload, err := crypto.DecompressZlib(decryptedPayload)
		if err != nil {
			return fmt.Errorf("failed to decompress events payload: %w", err)
		}

		// 6. Parse JSON events
		var container struct {
			DatabaseActivityEventList []map[string]interface{} `json:"databaseActivityEventList"`
		}
		if err := json.Unmarshal(decompressedPayload, &container); err != nil {
			return fmt.Errorf("failed to parse database activity events JSON: %w", err)
		}

		// 7. Write to analytical destination S3 for each matched filter
		defaultDestBucket := os.Getenv("DAS_OUTPUT_BUCKET")
		if defaultDestBucket == "" {
			defaultDestBucket = sourceBucket
		}

		defaultOutputKmsKeyID := os.Getenv("DAS_OUTPUT_KMS_KEY_ID")

		for _, f := range matchedFilters {
			activeFilterName := f.Name
			if activeFilterName == "" {
				activeFilterName = "default"
			}

			// Destination bucket resolution:
			// 1. Filter-level output.bucket
			// 2. Environment variable DAS_OUTPUT_BUCKET
			// 3. Incoming source S3 bucket
			activeDestBucket := defaultDestBucket
			if f.Output != nil && f.Output.Bucket != "" {
				activeDestBucket = f.Output.Bucket
			}
			if activeDestBucket == sourceBucket {
				slog.Warn("Source and destination buckets are identical; ensure S3 event notification prefix filters are configured to prevent recursive execution loops",
					"Filter", activeFilterName,
					"Bucket", sourceBucket,
				)
			}

			// KMS Key resolution:
			// 1. Filter-level output.kms_key_id
			// 2. Environment variable DAS_OUTPUT_KMS_KEY_ID
			activeKmsKeyID := defaultOutputKmsKeyID
			if f.Output != nil && f.Output.KmsKeyID != "" {
				activeKmsKeyID = f.Output.KmsKeyID
			}

			parquetBytes, rowCount, err := parquet.ConvertEventsToParquet(container.DatabaseActivityEventList, f)
			if err != nil {
				return fmt.Errorf("failed to convert events to Parquet for filter %q: %w", activeFilterName, err)
			}

			if rowCount == 0 {
				slog.Info("No events matched filter criteria, skipping S3 upload",
					"Filter", activeFilterName,
					"Key", key,
					"ResourceID", clusterID,
				)
				continue
			}

			destKey := BuildDestinationKey(key, clusterID, activeFilterName, f.Output)
			slog.Info("Writing processed data back to S3",
				"Filter", activeFilterName,
				"SourceBucket", sourceBucket,
				"DestBucket", activeDestBucket,
				"DestKey", destKey,
				"ResourceID", clusterID,
				"RowCount", rowCount,
			)

			putInput := &s3.PutObjectInput{
				Bucket: aws.String(activeDestBucket),
				Key:    aws.String(destKey),
				Body:   bytes.NewReader(parquetBytes),
			}

			if activeKmsKeyID != "" {
				putInput.ServerSideEncryption = s3types.ServerSideEncryptionAwsKms
				putInput.SSEKMSKeyId = aws.String(activeKmsKeyID)
			}

			if _, err = p.S3Client.PutObject(ctx, putInput); err != nil {
				return fmt.Errorf("failed to write processed data to %s/%s for filter %q: %w", activeDestBucket, destKey, activeFilterName, err)
			}
		}
	}

	return nil
}

// ExtractRDSResourceID dynamically discovers the RDS Resource ID from S3 key directory segments.
// It searches directory segments for Aurora DB cluster identifiers (prefixed with "cluster-")
// or RDS DB instance identifiers (prefixed with "db-"). The last segment (the object name itself)
// is excluded from matching regardless of whether it has a file suffix or not.
// If no matching segment is found, it returns the provided fallback value.
func ExtractRDSResourceID(s3Key, fallback string) string {
	parts := strings.Split(s3Key, "/")
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		if strings.HasPrefix(part, "cluster-") || strings.HasPrefix(part, "db-") {
			return part
		}
	}
	return fallback
}

// ResolveKMSRegion resolves the AWS region to use for KMS envelope decryption:
// 1. Filter-level kms_region configured on any matched filter
// 2. Fallback environment variable DAS_KMS_REGION_NAME
// If none of these specify a region, it returns an empty string (allowing AWS SDK v2 to use the ambient default).
func ResolveKMSRegion(matchedFilters []*filter.FilterConfig) string {
	for _, f := range matchedFilters {
		if f != nil && f.KMSRegion != "" {
			return f.KMSRegion
		}
	}
	if envRegion := os.Getenv("DAS_KMS_REGION_NAME"); envRegion != "" {
		return envRegion
	}
	return ""
}

// IsIgnoredS3Key returns true if the S3 key should be skipped by the processor.
// This safely ignores non-data delivery objects such as Amazon Data Firehose error delivery
// manifests (e.g. keys containing an "errors" or "error" directory segment).
func IsIgnoredS3Key(s3Key string) bool {
	cleanKey := strings.TrimPrefix(s3Key, "/")
	parts := strings.Split(cleanKey, "/")
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "errors" || parts[i] == "error" {
			return true
		}
	}
	return false
}

var dateRegex = regexp.MustCompile(`/(20\d\d)/([01]\d)/([0-3]\d)(?:/([0-2]\d))?`)

type dateComponents struct {
	Year  string
	Month string
	Day   string
	Hour  string
}

func extractDateComponents(s3Key string) dateComponents {
	now := time.Now().UTC()
	defaults := dateComponents{
		Year:  now.Format("2006"),
		Month: now.Format("01"),
		Day:   now.Format("02"),
		Hour:  now.Format("15"),
	}

	matches := dateRegex.FindStringSubmatch("/" + strings.TrimPrefix(s3Key, "/"))
	if len(matches) >= 4 && matches[1] != "" {
		defaults.Year = matches[1]
		defaults.Month = matches[2]
		defaults.Day = matches[3]
		if len(matches) >= 5 && matches[4] != "" {
			defaults.Hour = matches[4]
		}
	}
	return defaults
}

// BuildDestinationKey constructs the partitioned S3 destination key based on format rules.
// Supported formats:
// - "hive": [prefix/][<filter>/]cluster=<cluster-id>/year=YYYY/month=MM/day=DD/hour=HH/<basename>.parquet
// - "date": [prefix/][<filter>/]<cluster-id>/YYYY/MM/DD/HH/<basename>.parquet
// - "mirrored": [prefix/][<filter>/]<source-key-trimmed>-processed.parquet
func BuildDestinationKey(s3Key, clusterID, filterName string, outCfg *filter.OutputConfig) string {
	format := "hive"
	prefix := "das"
	includeFilter := false

	// Config from filter file
	if outCfg != nil {
		if outCfg.Format != "" {
			format = strings.ToLower(outCfg.Format)
		}
		if outCfg.Prefix != nil {
			prefix = strings.Trim(*outCfg.Prefix, "/")
		}
		includeFilter = outCfg.IncludeFilter
	}

	// Environment variable overrides
	if envFormat := os.Getenv("DAS_OUTPUT_FORMAT"); envFormat != "" {
		format = strings.ToLower(envFormat)
	}
	if envPrefix, exists := os.LookupEnv("DAS_OUTPUT_PREFIX"); exists {
		prefix = strings.Trim(envPrefix, "/")
	}
	if envIncludeFilter := os.Getenv("DAS_OUTPUT_INCLUDE_FILTER"); envIncludeFilter != "" {
		includeFilter = envIncludeFilter == "true" || envIncludeFilter == "1"
	}

	// Base path building
	var basePathSegments []string
	if prefix != "" {
		basePathSegments = append(basePathSegments, prefix)
	}
	if includeFilter && filterName != "" {
		basePathSegments = append(basePathSegments, filterName)
	}

	cleanBase := strings.TrimSuffix(path.Base(s3Key), ".parquet")
	cleanBase = strings.TrimSuffix(cleanBase, ".gz")
	cleanBase = strings.TrimSuffix(cleanBase, ".json")
	if cleanBase == "" || cleanBase == "." {
		cleanBase = "das-processed"
	}

	switch format {
	case "mirrored", "mirror":
		// Preserves source key directory structure
		sourceDir := path.Dir(s3Key)
		destFileName := fmt.Sprintf("%s-processed.parquet", cleanBase)
		if sourceDir == "." || sourceDir == "" {
			basePathSegments = append(basePathSegments, destFileName)
		} else {
			basePathSegments = append(basePathSegments, sourceDir, destFileName)
		}
		return path.Join(basePathSegments...)

	case "date":
		// <cluster-id>/YYYY/MM/DD/HH/<basename>.parquet
		dates := extractDateComponents(s3Key)
		basePathSegments = append(basePathSegments,
			clusterID,
			dates.Year,
			dates.Month,
			dates.Day,
			dates.Hour,
			fmt.Sprintf("%s.parquet", cleanBase),
		)
		return path.Join(basePathSegments...)

	case "hive":
		fallthrough
	default:
		// cluster=<cluster-id>/year=YYYY/month=MM/day=DD/hour=HH/<basename>.parquet
		dates := extractDateComponents(s3Key)
		basePathSegments = append(basePathSegments,
			fmt.Sprintf("cluster=%s", clusterID),
			fmt.Sprintf("year=%s", dates.Year),
			fmt.Sprintf("month=%s", dates.Month),
			fmt.Sprintf("day=%s", dates.Day),
			fmt.Sprintf("hour=%s", dates.Hour),
			fmt.Sprintf("%s.parquet", cleanBase),
		)
		return path.Join(basePathSegments...)
	}
}
