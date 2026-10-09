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
// preloaded configs, or auto-discovering from FiltersDir.
func (p *Processor) ResolveFiltersForEvent(clusterID, s3Key string, overrideFilters []*filter.FilterConfig) []*filter.FilterConfig {
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
	return filter.ResolveFilters(configs, clusterID, s3Key)
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

// ProcessMessage handles the complete pipeline for a single SQS message, applying all matched filters
// to the decrypted and decompressed payload in memory.
func (p *Processor) ProcessMessage(ctx context.Context, message events.SQSMessage, rdsResourceID string, customFilters ...*filter.FilterConfig) error {
	var s3Event model.S3EventNotification

	// Check if it's an SNS wrapped message
	var snsMsg struct {
		Type    string `json:"Type"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal([]byte(message.Body), &snsMsg); err == nil && snsMsg.Type == "Notification" && snsMsg.Message != "" {
		if err := json.Unmarshal([]byte(snsMsg.Message), &s3Event); err != nil {
			return fmt.Errorf("failed to parse S3 event from SNS message: %w", err)
		}
	} else {
		if err := json.Unmarshal([]byte(message.Body), &s3Event); err != nil {
			return fmt.Errorf("failed to parse S3 event: %w", err)
		}
	}

	for _, record := range s3Event.Records {
		sourceBucket := record.S3.Bucket.Name
		key := record.S3.Object.Key

		clusterID := ExtractRDSResourceID(key, rdsResourceID)
		if clusterID == "" {
			return fmt.Errorf("missing RDS resource ID: unable to extract from S3 key %q and no fallback DAS_RDS_RESOURCE_ID configured", key)
		}

		matchedFilters := p.ResolveFiltersForEvent(clusterID, key, customFilters)
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

		decryptOutput, err := p.KMSClient.Decrypt(ctx, &kms.DecryptInput{
			CiphertextBlob: decodedKmsKey,
			EncryptionContext: map[string]string{
				"aws:rds:dbc-id": clusterID,
			},
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
		destBucket := os.Getenv("DAS_OUTPUT_BUCKET")
		if destBucket == "" {
			destBucket = sourceBucket
		}
		if destBucket == sourceBucket {
			slog.Warn("Source and destination buckets are identical; ensure S3 event notification prefix filters are configured to prevent recursive execution loops", "Bucket", sourceBucket)
		}

		outputKmsKeyID := os.Getenv("DAS_OUTPUT_KMS_KEY_ID")

		for _, f := range matchedFilters {
			activeFilterName := f.Name
			if activeFilterName == "" {
				activeFilterName = "default"
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
				"DestBucket", destBucket,
				"DestKey", destKey,
				"ResourceID", clusterID,
				"RowCount", rowCount,
			)

			putInput := &s3.PutObjectInput{
				Bucket: aws.String(destBucket),
				Key:    aws.String(destKey),
				Body:   bytes.NewReader(parquetBytes),
			}

			if outputKmsKeyID != "" {
				putInput.ServerSideEncryption = s3types.ServerSideEncryptionAwsKms
				putInput.SSEKMSKeyId = aws.String(outputKmsKeyID)
			}

			if _, err = p.S3Client.PutObject(ctx, putInput); err != nil {
				return fmt.Errorf("failed to write processed data to %s/%s for filter %q: %w", destBucket, destKey, activeFilterName, err)
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
