package processor

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	mpl "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygenerated"
	mpltypes "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygeneratedtypes"
	client "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygenerated"
	esdktypes "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygeneratedtypes"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/divmora/aws-das-processor/pkg/filter"
	"github.com/divmora/aws-das-processor/pkg/model"
)

type mockS3Client struct {
	getObjectFunc func(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	putObjectFunc func(ctx context.Context, in *s3.PutObjectInput, opt ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

func (m *mockS3Client) GetObject(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if m.getObjectFunc != nil {
		return m.getObjectFunc(ctx, in, opt...)
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader([]byte("{}"))),
	}, nil
}

func (m *mockS3Client) PutObject(ctx context.Context, in *s3.PutObjectInput, opt ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if m.putObjectFunc != nil {
		return m.putObjectFunc(ctx, in, opt...)
	}
	return &s3.PutObjectOutput{}, nil
}

type mockKMSClient struct {
	decryptFunc func(ctx context.Context, in *kms.DecryptInput, opt ...func(*kms.Options)) (*kms.DecryptOutput, error)
}

func (m *mockKMSClient) Decrypt(ctx context.Context, in *kms.DecryptInput, opt ...func(*kms.Options)) (*kms.DecryptOutput, error) {
	if m.decryptFunc != nil {
		return m.decryptFunc(ctx, in, opt...)
	}
	return &kms.DecryptOutput{
		Plaintext: []byte("01234567890123456789012345678901"),
	}, nil
}

func TestExtractRDSResourceID(t *testing.T) {
	tests := []struct {
		name     string
		s3Key    string
		fallback string
		want     string
	}{
		{
			name:     "Firehose prefix with Aurora cluster",
			s3Key:    "firehose/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-sample-prod-primary-1-2026-10-01-05-50-52-11111111-2222-3333-4444-555555555555",
			fallback: "cluster-fallback",
			want:     "cluster-EXAMPLE1234567890ABCDEF",
		},
		{
			name:     "AWS native RDS DAS prefix",
			s3Key:    "AWSLogs/123456789012/RDS/us-east-1/cluster-ABCD/2026/09/29/das-data.json.gz",
			fallback: "",
			want:     "cluster-ABCD",
		},
		{
			name:     "Firehose destination with AWSLogs DAS prefix",
			s3Key:    "AWSLogs/123456789012/DAS/us-east-1/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-file.json",
			fallback: "cluster-fallback",
			want:     "cluster-EXAMPLE1234567890ABCDEF",
		},
		{
			name:     "Standalone RDS instance prefix",
			s3Key:    "custom-prefix/logs/db-ABCDEFGHIJKLMNOPQRSTUVWXYZ/2026/10/01/data.gz",
			fallback: "cluster-fallback",
			want:     "db-ABCDEFGHIJKLMNOPQRSTUVWXYZ",
		},
		{
			name:     "File name starting with cluster- and extension is ignored",
			s3Key:    "my-bucket-prefix/2026/10/01/cluster-backup.json",
			fallback: "fallback-cluster-id",
			want:     "fallback-cluster-id",
		},
		{
			name:     "File name without extension starting with cluster- is ignored",
			s3Key:    "my-bucket-prefix/2026/10/01/cluster-raw-dump-no-ext",
			fallback: "fallback-cluster-id",
			want:     "fallback-cluster-id",
		},
		{
			name:     "File name without extension starting with db- is ignored",
			s3Key:    "my-bucket-prefix/2026/10/01/db-audit-no-ext",
			fallback: "fallback-cluster-id",
			want:     "fallback-cluster-id",
		},
		{
			name:     "File delivered without extension in valid cluster directory",
			s3Key:    "firehose/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-file-without-any-extension",
			fallback: "fallback-cluster-id",
			want:     "cluster-EXAMPLE1234567890ABCDEF",
		},
		{
			name:     "Root level object with no directory prefix",
			s3Key:    "cluster-root-file-no-ext",
			fallback: "fallback-cluster-id",
			want:     "fallback-cluster-id",
		},
		{
			name:     "No cluster or db prefix returns fallback",
			s3Key:    "plain-logs/2026/10/01/stream.json",
			fallback: "cluster-configured-fallback",
			want:     "cluster-configured-fallback",
		},
		{
			name:     "No cluster or db prefix and empty fallback",
			s3Key:    "plain-logs/2026/10/01/stream.json",
			fallback: "",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractRDSResourceID(tt.s3Key, tt.fallback)
			if got != tt.want {
				t.Errorf("ExtractRDSResourceID(%q, %q) = %q, want %q", tt.s3Key, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestResolveKMSRegion(t *testing.T) {
	tests := []struct {
		name           string
		matchedFilters []*filter.FilterConfig
		envVal         string
		want           string
	}{
		{
			name: "Filter kms_region takes precedence over env var",
			matchedFilters: []*filter.FilterConfig{
				{Name: "filter1", KMSRegion: "eu-west-1"},
			},
			envVal: "ap-southeast-1",
			want:   "eu-west-1",
		},
		{
			name: "Env variable used when filter kms_region is empty",
			matchedFilters: []*filter.FilterConfig{
				{Name: "filter1", KMSRegion: ""},
			},
			envVal: "us-west-2",
			want:   "us-west-2",
		},
		{
			name:           "Empty string returned when no filter and no env var",
			matchedFilters: []*filter.FilterConfig{},
			envVal:         "",
			want:           "",
		},
		{
			name: "Multiple filters where first filter has no region and second has region",
			matchedFilters: []*filter.FilterConfig{
				{Name: "filter1", KMSRegion: ""},
				{Name: "filter2", KMSRegion: "ca-central-1"},
			},
			envVal: "us-east-1",
			want:   "ca-central-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVal != "" {
				t.Setenv("DAS_KMS_REGION_NAME", tt.envVal)
			} else {
				t.Setenv("DAS_KMS_REGION_NAME", "")
			}
			got := ResolveKMSRegion(tt.matchedFilters)
			if got != tt.want {
				t.Errorf("ResolveKMSRegion(filters) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsIgnoredS3Key(t *testing.T) {
	tests := []struct {
		name  string
		s3Key string
		want  bool
	}{
		{
			name:  "Firehose standard AWSLogs DAS error output prefix",
			s3Key: "AWSLogs/102147727378/DAS/ap-south-1/cluster-JAWX76GRVX64PXJZ5T56KUYHYQ/errors/2026/10/10/ProcessingFailed-123.json",
			want:  true,
		},
		{
			name:  "Firehose generic error prefix",
			s3Key: "firehose/cluster-EXAMPLE1234567890ABCDEF/errors/2026/10/01/05/err-manifest.json",
			want:  true,
		},
		{
			name:  "Root-level errors directory",
			s3Key: "errors/2026/10/01/file.json",
			want:  true,
		},
		{
			name:  "Custom prefix with error folder",
			s3Key: "my-prefix/error/2026/10/01/manifest.json",
			want:  true,
		},
		{
			name:  "Valid standard data key with AWSLogs DAS prefix",
			s3Key: "AWSLogs/102147727378/DAS/ap-south-1/cluster-JAWX76GRVX64PXJZ5T56KUYHYQ/2026/10/10/das-file.json",
			want:  false,
		},
		{
			name:  "Valid firehose data key",
			s3Key: "firehose/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-events.json",
			want:  false,
		},
		{
			name:  "Cluster name containing errors substring is not ignored",
			s3Key: "firehose/cluster-errors-prod/2026/10/01/05/das-events.json",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsIgnoredS3Key(tt.s3Key)
			if got != tt.want {
				t.Errorf("IsIgnoredS3Key(%q) = %v, want %v", tt.s3Key, got, tt.want)
			}
		})
	}
}

func TestParseS3Records(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    []S3RecordRef
		wantErr bool
	}{
		{
			name: "EventBridge S3 notification (modern standard)",
			body: `{
				"version": "0",
				"id": "17793124-05d5-b190-f004-9f1244104aa4",
				"detail-type": "Object Created",
				"source": "aws.s3",
				"account": "123456789012",
				"time": "2026-10-12T00:37:34Z",
				"region": "us-east-1",
				"resources": ["arn:aws:s3:::raw-das-bucket"],
				"detail": {
					"version": "0",
					"bucket": {"name": "raw-das-bucket"},
					"object": {"key": "AWSLogs/123456789012/DAS/us-east-1/cluster-ABCD/2026/10/12/file.json"}
				}
			}`,
			want: []S3RecordRef{{
				Bucket: "raw-das-bucket",
				Key:    "AWSLogs/123456789012/DAS/us-east-1/cluster-ABCD/2026/10/12/file.json",
			}},
			wantErr: false,
		},
		{
			name: "SNS wrapped S3 notification",
			body: `{
				"Type": "Notification",
				"MessageId": "msg-123",
				"TopicArn": "arn:aws:sns:us-east-1:123456789012:das-topic",
				"Message": "{\"Records\":[{\"s3\":{\"bucket\":{\"name\":\"raw-source-bucket\"},\"object\":{\"key\":\"firehose/cluster-ABCD/data.json\"}}}]}"
			}`,
			want: []S3RecordRef{{
				Bucket: "raw-source-bucket",
				Key:    "firehose/cluster-ABCD/data.json",
			}},
			wantErr: false,
		},
		{
			name: "SNS wrapped EventBridge S3 notification",
			body: `{
				"Type": "Notification",
				"MessageId": "msg-123",
				"TopicArn": "arn:aws:sns:us-east-1:123456789012:das-topic",
				"Message": "{\"source\":\"aws.s3\",\"detail-type\":\"Object Created\",\"detail\":{\"bucket\":{\"name\":\"eb-sns-bucket\"},\"object\":{\"key\":\"data.json\"}}}"
			}`,
			want: []S3RecordRef{{
				Bucket: "eb-sns-bucket",
				Key:    "data.json",
			}},
			wantErr: false,
		},
		{
			name: "Direct S3 notification",
			body: `{"Records":[{"s3":{"bucket":{"name":"direct-s3-bucket"},"object":{"key":"direct/file.json"}}}]}`,
			want: []S3RecordRef{{
				Bucket: "direct-s3-bucket",
				Key:    "direct/file.json",
			}},
			wantErr: false,
		},
		{
			name:    "Direct Amazon S3 test notification (s3:TestEvent)",
			body:    `{"Service":"Amazon S3","Event":"s3:TestEvent","Time":"2026-10-12T00:00:00.000Z","Bucket":"my-test-bucket","RequestId":"REQ123","HostId":"HOST456"}`,
			want:    []S3RecordRef{},
			wantErr: false,
		},
		{
			name: "SNS wrapped Amazon S3 test notification (s3:TestEvent)",
			body: `{
				"Type": "Notification",
				"MessageId": "msg-test-123",
				"TopicArn": "arn:aws:sns:us-east-1:123456789012:das-topic",
				"Message": "{\"Service\":\"Amazon S3\",\"Event\":\"s3:TestEvent\",\"Bucket\":\"my-test-bucket\"}"
			}`,
			want:    []S3RecordRef{},
			wantErr: false,
		},
		{
			name:    "Unrecognized / invalid payload format",
			body:    `{"unrelated":"json"}`,
			want:    nil,
			wantErr: true,
		},
		{
			name:    "Malformed JSON string",
			body:    `{not valid json`,
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseS3Records(tt.body)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseS3Records() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if len(got) != len(tt.want) {
					t.Fatalf("ParseS3Records() len = %d, want %d", len(got), len(tt.want))
				}
				for i := range got {
					if got[i].Bucket != tt.want[i].Bucket || got[i].Key != tt.want[i].Key {
						t.Errorf("ParseS3Records()[%d] = %+v, want %+v", i, got[i], tt.want[i])
					}
				}
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}

func TestBuildDestinationKey(t *testing.T) {
	sampleKey := "firehose/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-file.json.gz"
	clusterID := "cluster-EXAMPLE1234567890ABCDEF"

	tests := []struct {
		name       string
		s3Key      string
		clusterID  string
		filterName string
		outCfg     *filter.OutputConfig
		want       string
	}{
		{
			name:       "Hive format with default include_filter false",
			s3Key:      sampleKey,
			clusterID:  clusterID,
			filterName: "default",
			outCfg: &filter.OutputConfig{
				Format: "hive",
				Prefix: strPtr("das"),
			},
			want: "das/cluster=cluster-EXAMPLE1234567890ABCDEF/year=2026/month=10/day=01/hour=05/das-file.parquet",
		},
		{
			name:       "Hive format with include_filter true",
			s3Key:      sampleKey,
			clusterID:  clusterID,
			filterName: "compliance",
			outCfg: &filter.OutputConfig{
				Format:        "hive",
				Prefix:        strPtr("analytics"),
				IncludeFilter: true,
			},
			want: "analytics/compliance/cluster=cluster-EXAMPLE1234567890ABCDEF/year=2026/month=10/day=01/hour=05/das-file.parquet",
		},
		{
			name:       "Date format without filter in path",
			s3Key:      sampleKey,
			clusterID:  clusterID,
			filterName: "default",
			outCfg: &filter.OutputConfig{
				Format:        "date",
				Prefix:        strPtr("datalake"),
				IncludeFilter: false,
			},
			want: "datalake/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-file.parquet",
		},
		{
			name:       "Date format with filter in path",
			s3Key:      sampleKey,
			clusterID:  clusterID,
			filterName: "secops",
			outCfg: &filter.OutputConfig{
				Format:        "date",
				Prefix:        strPtr("datalake"),
				IncludeFilter: true,
			},
			want: "datalake/secops/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-file.parquet",
		},
		{
			name:       "Mirrored format with filter in path",
			s3Key:      sampleKey,
			clusterID:  clusterID,
			filterName: "default",
			outCfg: &filter.OutputConfig{
				Format:        "mirrored",
				Prefix:        strPtr("das"),
				IncludeFilter: true,
			},
			want: "das/default/firehose/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-file-processed.parquet",
		},
		{
			name:       "Mirrored format without filter in path",
			s3Key:      sampleKey,
			clusterID:  clusterID,
			filterName: "default",
			outCfg: &filter.OutputConfig{
				Format:        "mirrored",
				Prefix:        strPtr("das"),
				IncludeFilter: false,
			},
			want: "das/firehose/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-file-processed.parquet",
		},
		{
			name:       "Root prefix empty with Hive format",
			s3Key:      sampleKey,
			clusterID:  clusterID,
			filterName: "default",
			outCfg: &filter.OutputConfig{
				Format: "hive",
				Prefix: strPtr(""),
			},
			want: "cluster=cluster-EXAMPLE1234567890ABCDEF/year=2026/month=10/day=01/hour=05/das-file.parquet",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildDestinationKey(tt.s3Key, tt.clusterID, tt.filterName, tt.outCfg)
			if got != tt.want {
				t.Errorf("BuildDestinationKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildDestinationKey_EnvOverrides(t *testing.T) {
	t.Setenv("DAS_OUTPUT_FORMAT", "date")
	t.Setenv("DAS_OUTPUT_PREFIX", "env-prefix")
	t.Setenv("DAS_OUTPUT_INCLUDE_FILTER", "true")

	got := BuildDestinationKey("firehose/cluster-XYZ/2026/10/01/05/data.json", "cluster-XYZ", "audit", nil)
	expected := "env-prefix/audit/cluster-XYZ/2026/10/01/05/data.parquet"
	if got != expected {
		t.Errorf("expected env override key %q, got %q", expected, got)
	}
}

func TestProcessor_ProcessSQSEvent_PartialBatchFailure(t *testing.T) {
	ctx := context.Background()
	p := New(&mockS3Client{}, &mockKMSClient{})

	sqsEvent := events.SQSEvent{
		Records: []events.SQSMessage{
			{MessageId: "msg-fail", Body: "invalid json"},
		},
	}

	res, err := p.ProcessSQSEvent(ctx, sqsEvent, "cluster-XYZ")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(res.BatchItemFailures) != 1 || res.BatchItemFailures[0].ItemIdentifier != "msg-fail" {
		t.Errorf("expected batch failure for msg-fail, got %+v", res.BatchItemFailures)
	}
}

func TestProcessor_ProcessMessage_MissingResourceID(t *testing.T) {
	ctx := context.Background()
	p := New(&mockS3Client{}, &mockKMSClient{})

	s3NotifJSON := fmt.Sprintf(`{"Records":[{"s3":{"bucket":{"name":%q},"object":{"key":%q}}}]}`, "source-bucket", "plain-prefix/no-cluster.json")
	msg := events.SQSMessage{
		MessageId: "msg-no-res-id",
		Body:      s3NotifJSON,
	}

	err := p.ProcessMessage(ctx, msg, "")
	if err == nil || !strings.Contains(err.Error(), "missing RDS resource ID") {
		t.Fatalf("expected missing resource ID error, got %v", err)
	}
}

func TestProcessor_EndToEnd_SeparateDestBucket(t *testing.T) {
	ctx := context.Background()

	key := []byte("01234567890123456789012345678901")
	sampleEvents := `{"type":"DatabaseActivityMonitoringRecords","version":"1.0","databaseActivityEventList":[{"command":"SELECT 1"}]}`
	var b bytes.Buffer
	zw := zlib.NewWriter(&b)
	zw.Write([]byte(sampleEvents))
	zw.Close()

	matProv, err := mpl.NewClient(mpltypes.MaterialProvidersConfig{})
	if err != nil {
		t.Fatalf("failed to create material providers: %v", err)
	}
	aesKeyring, err := matProv.CreateRawAesKeyring(ctx, mpltypes.CreateRawAesKeyringInput{
		KeyName:      "DataKey",
		KeyNamespace: "RawMasterKeyProvider",
		WrappingKey:  key,
		WrappingAlg:  mpltypes.AesWrappingAlgAlgAes256GcmIv12Tag16,
	})
	if err != nil {
		t.Fatalf("failed to create raw aes keyring: %v", err)
	}
	encClient, err := client.NewClient(esdktypes.AwsEncryptionSdkConfig{})
	if err != nil {
		t.Fatalf("failed to create encryption sdk client: %v", err)
	}
	encRes, err := encClient.Encrypt(ctx, esdktypes.EncryptInput{
		Plaintext: b.Bytes(),
		Keyring:   aesKeyring,
	})
	if err != nil {
		t.Fatalf("failed to encrypt payload: %v", err)
	}

	dasPayload := model.DASPayload{
		Type:                   "DatabaseActivityMonitoringRecords",
		Version:                "1.0",
		Key:                    base64.StdEncoding.EncodeToString([]byte("mock-kms-key")),
		DatabaseActivityEvents: base64.StdEncoding.EncodeToString(encRes.Ciphertext),
	}
	dasPayloadBytes, _ := json.Marshal(dasPayload)

	var writtenBucket, writtenKey string
	mockS3 := &mockS3Client{
		getObjectFunc: func(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(bytes.NewReader(dasPayloadBytes)),
			}, nil
		},
		putObjectFunc: func(ctx context.Context, in *s3.PutObjectInput, opt ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			writtenBucket = *in.Bucket
			writtenKey = *in.Key
			return &s3.PutObjectOutput{}, nil
		},
	}
	mockKMS := &mockKMSClient{
		decryptFunc: func(ctx context.Context, in *kms.DecryptInput, opt ...func(*kms.Options)) (*kms.DecryptOutput, error) {
			if in.EncryptionContext["aws:rds:dbc-id"] != "cluster-EXAMPLE1234567890ABCDEF" {
				return nil, errors.New("mismatched encryption context")
			}
			return &kms.DecryptOutput{
				Plaintext: key,
			}, nil
		},
	}

	p := New(mockS3, mockKMS)

	t.Setenv("DAS_OUTPUT_BUCKET", "analytical-destination-bucket")

	firehoseKey := "firehose/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-events.json"
	s3NotifJSON := fmt.Sprintf(`{"Records":[{"s3":{"bucket":{"name":%q},"object":{"key":%q}}}]}`, "raw-source-bucket", firehoseKey)
	msg := events.SQSMessage{
		MessageId: "msg-e2e",
		Body:      s3NotifJSON,
	}

	filterCfg := &filter.FilterConfig{
		Name:  "default",
		Drop:  []string{},
		Query: map[string]interface{}{},
		Output: &filter.OutputConfig{
			Format:        "hive",
			Prefix:        strPtr("das"),
			IncludeFilter: false,
		},
	}

	err = p.ProcessMessage(ctx, msg, "", filterCfg)
	if err != nil {
		t.Fatalf("expected successful message processing, got: %v", err)
	}

	if writtenBucket != "analytical-destination-bucket" {
		t.Errorf("expected writtenBucket to be 'analytical-destination-bucket', got %q", writtenBucket)
	}

	expectedKey := "das/cluster=cluster-EXAMPLE1234567890ABCDEF/year=2026/month=10/day=01/hour=05/das-events.parquet"
	if writtenKey != expectedKey {
		t.Errorf("expected writtenKey to be %q, got %q", expectedKey, writtenKey)
	}
}

func TestProcessor_EndToEnd_MultiFilterSingleDecrypt(t *testing.T) {
	ctx := context.Background()

	key := []byte("01234567890123456789012345678901")
	sampleEvents := `{
		"type":"DatabaseActivityMonitoringRecords",
		"version":"1.0",
		"databaseActivityEventList":[
			{"command":"SELECT 1", "dbUserName":"app_user", "clientMac":"00:11:22:33:44:55"},
			{"command":"DROP TABLE users", "dbUserName":"admin_user"}
		]
	}`
	var b bytes.Buffer
	zw := zlib.NewWriter(&b)
	zw.Write([]byte(sampleEvents))
	zw.Close()

	matProv, err := mpl.NewClient(mpltypes.MaterialProvidersConfig{})
	if err != nil {
		t.Fatalf("failed to create material providers: %v", err)
	}
	aesKeyring, err := matProv.CreateRawAesKeyring(ctx, mpltypes.CreateRawAesKeyringInput{
		KeyName:      "DataKey",
		KeyNamespace: "RawMasterKeyProvider",
		WrappingKey:  key,
		WrappingAlg:  mpltypes.AesWrappingAlgAlgAes256GcmIv12Tag16,
	})
	if err != nil {
		t.Fatalf("failed to create raw aes keyring: %v", err)
	}
	encClient, err := client.NewClient(esdktypes.AwsEncryptionSdkConfig{})
	if err != nil {
		t.Fatalf("failed to create encryption sdk client: %v", err)
	}
	encRes, err := encClient.Encrypt(ctx, esdktypes.EncryptInput{
		Plaintext: b.Bytes(),
		Keyring:   aesKeyring,
	})
	if err != nil {
		t.Fatalf("failed to encrypt payload: %v", err)
	}

	dasPayload := model.DASPayload{
		Type:                   "DatabaseActivityMonitoringRecords",
		Version:                "1.0",
		Key:                    base64.StdEncoding.EncodeToString([]byte("mock-kms-key")),
		DatabaseActivityEvents: base64.StdEncoding.EncodeToString(encRes.Ciphertext),
	}
	dasPayloadBytes, _ := json.Marshal(dasPayload)

	getObjectCalls := 0
	kmsDecryptCalls := 0
	var writtenKeys []string

	mockS3 := &mockS3Client{
		getObjectFunc: func(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			getObjectCalls++
			return &s3.GetObjectOutput{
				Body: io.NopCloser(bytes.NewReader(dasPayloadBytes)),
			}, nil
		},
		putObjectFunc: func(ctx context.Context, in *s3.PutObjectInput, opt ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			writtenKeys = append(writtenKeys, *in.Key)
			return &s3.PutObjectOutput{}, nil
		},
	}
	mockKMS := &mockKMSClient{
		decryptFunc: func(ctx context.Context, in *kms.DecryptInput, opt ...func(*kms.Options)) (*kms.DecryptOutput, error) {
			kmsDecryptCalls++
			return &kms.DecryptOutput{
				Plaintext: key,
			}, nil
		},
	}

	p := New(mockS3, mockKMS)

	complianceFilter := &filter.FilterConfig{
		Name: "compliance",
		Match: &filter.MatchConfig{
			Clusters: []string{"cluster-EXAMPLE*"},
		},
		Drop:  []string{"clientMac"},
		Query: map[string]interface{}{},
		Output: &filter.OutputConfig{
			Format:        "hive",
			Prefix:        strPtr("das"),
			IncludeFilter: true,
		},
	}

	securityFilter := &filter.FilterConfig{
		Name: "security-ddl",
		Match: &filter.MatchConfig{
			Clusters: []string{"cluster-EXAMPLE*"},
			Paths:    []string{"firehose/*"},
		},
		Drop: []string{},
		Query: map[string]interface{}{
			"type":      "selector",
			"dimension": "command",
			"value":     "DROP TABLE users",
		},
		Output: &filter.OutputConfig{
			Format:        "date",
			Prefix:        strPtr("secops"),
			IncludeFilter: true,
		},
	}

	p.SetFilterConfigs([]*filter.FilterConfig{complianceFilter, securityFilter})

	firehoseKey := "firehose/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-events.json"
	s3NotifJSON := fmt.Sprintf(`{"Records":[{"s3":{"bucket":{"name":%q},"object":{"key":%q}}}]}`, "raw-source-bucket", firehoseKey)
	msg := events.SQSMessage{
		MessageId: "msg-multi-e2e",
		Body:      s3NotifJSON,
	}

	// Run with auto-discovery
	err = p.ProcessMessage(ctx, msg, "")
	if err != nil {
		t.Fatalf("expected successful multi-filter execution, got: %v", err)
	}

	// Verify single decrypt and single download
	if getObjectCalls != 1 {
		t.Errorf("expected exactly 1 S3 GetObject call, got %d", getObjectCalls)
	}
	if kmsDecryptCalls != 1 {
		t.Errorf("expected exactly 1 KMS Decrypt call, got %d", kmsDecryptCalls)
	}

	// Verify 2 distinct Parquet files written for the 2 matching filters
	if len(writtenKeys) != 2 {
		t.Fatalf("expected 2 Parquet files written to S3, got %d: %+v", len(writtenKeys), writtenKeys)
	}

	expectedComplianceKey := "das/compliance/cluster=cluster-EXAMPLE1234567890ABCDEF/year=2026/month=10/day=01/hour=05/das-events.parquet"
	expectedSecurityKey := "secops/security-ddl/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-events.parquet"

	if writtenKeys[0] != expectedComplianceKey {
		t.Errorf("expected key[0]=%q, got %q", expectedComplianceKey, writtenKeys[0])
	}
	if writtenKeys[1] != expectedSecurityKey {
		t.Errorf("expected key[1]=%q, got %q", expectedSecurityKey, writtenKeys[1])
	}
}

func TestProcessor_EndToEnd_ZeroRowsSkipped(t *testing.T) {
	ctx := context.Background()

	key := []byte("01234567890123456789012345678901")
	sampleEvents := `{"type":"DatabaseActivityMonitoringRecords","version":"1.0","databaseActivityEventList":[{"command":"SELECT 1"}]}`
	var b bytes.Buffer
	zw := zlib.NewWriter(&b)
	zw.Write([]byte(sampleEvents))
	zw.Close()

	matProv, _ := mpl.NewClient(mpltypes.MaterialProvidersConfig{})
	aesKeyring, _ := matProv.CreateRawAesKeyring(ctx, mpltypes.CreateRawAesKeyringInput{
		KeyName:      "DataKey",
		KeyNamespace: "RawMasterKeyProvider",
		WrappingKey:  key,
		WrappingAlg:  mpltypes.AesWrappingAlgAlgAes256GcmIv12Tag16,
	})
	encClient, _ := client.NewClient(esdktypes.AwsEncryptionSdkConfig{})
	encRes, _ := encClient.Encrypt(ctx, esdktypes.EncryptInput{
		Plaintext: b.Bytes(),
		Keyring:   aesKeyring,
	})

	dasPayload := model.DASPayload{
		Type:                   "DatabaseActivityMonitoringRecords",
		Version:                "1.0",
		Key:                    base64.StdEncoding.EncodeToString([]byte("mock-kms-key")),
		DatabaseActivityEvents: base64.StdEncoding.EncodeToString(encRes.Ciphertext),
	}
	dasPayloadBytes, _ := json.Marshal(dasPayload)

	putCalls := 0
	mockS3 := &mockS3Client{
		getObjectFunc: func(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(bytes.NewReader(dasPayloadBytes)),
			}, nil
		},
		putObjectFunc: func(ctx context.Context, in *s3.PutObjectInput, opt ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			putCalls++
			return &s3.PutObjectOutput{}, nil
		},
	}
	mockKMS := &mockKMSClient{
		decryptFunc: func(ctx context.Context, in *kms.DecryptInput, opt ...func(*kms.Options)) (*kms.DecryptOutput, error) {
			return &kms.DecryptOutput{Plaintext: key}, nil
		},
	}

	p := New(mockS3, mockKMS)

	// Filter query will match nothing
	noMatchFilter := &filter.FilterConfig{
		Name: "no-match",
		Query: map[string]interface{}{
			"type":      "selector",
			"dimension": "command",
			"value":     "NON_EXISTENT_COMMAND",
		},
	}

	firehoseKey := "firehose/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-events.json"
	s3NotifJSON := fmt.Sprintf(`{"Records":[{"s3":{"bucket":{"name":%q},"object":{"key":%q}}}]}`, "raw-source-bucket", firehoseKey)
	msg := events.SQSMessage{
		MessageId: "msg-zero-rows",
		Body:      s3NotifJSON,
	}

	err := p.ProcessMessage(ctx, msg, "", noMatchFilter)
	if err != nil {
		t.Fatalf("expected nil error when 0 rows match, got: %v", err)
	}

	if putCalls != 0 {
		t.Errorf("expected 0 PutObject calls when all rows filtered out, got %d", putCalls)
	}
}

func TestProcessor_EndToEnd_PerFilterDestinationBucketAndKMSKey(t *testing.T) {
	ctx := context.Background()

	key := []byte("01234567890123456789012345678901")
	sampleEvents := `{
		"type":"DatabaseActivityMonitoringRecords",
		"version":"1.0",
		"databaseActivityEventList":[
			{"command":"SELECT 1", "dbUserName":"app_user"}
		]
	}`
	var b bytes.Buffer
	zw := zlib.NewWriter(&b)
	zw.Write([]byte(sampleEvents))
	zw.Close()

	matProv, _ := mpl.NewClient(mpltypes.MaterialProvidersConfig{})
	aesKeyring, _ := matProv.CreateRawAesKeyring(ctx, mpltypes.CreateRawAesKeyringInput{
		KeyName:      "DataKey",
		KeyNamespace: "RawMasterKeyProvider",
		WrappingKey:  key,
		WrappingAlg:  mpltypes.AesWrappingAlgAlgAes256GcmIv12Tag16,
	})
	encClient, _ := client.NewClient(esdktypes.AwsEncryptionSdkConfig{})
	encRes, _ := encClient.Encrypt(ctx, esdktypes.EncryptInput{
		Plaintext: b.Bytes(),
		Keyring:   aesKeyring,
	})

	dasPayload := model.DASPayload{
		Type:                   "DatabaseActivityMonitoringRecords",
		Version:                "1.0",
		Key:                    base64.StdEncoding.EncodeToString([]byte("mock-kms-key")),
		DatabaseActivityEvents: base64.StdEncoding.EncodeToString(encRes.Ciphertext),
	}
	dasPayloadBytes, _ := json.Marshal(dasPayload)

	type putCallRecord struct {
		bucket   string
		key      string
		kmsKeyID string
	}
	var putRecords []putCallRecord

	mockS3 := &mockS3Client{
		getObjectFunc: func(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(bytes.NewReader(dasPayloadBytes)),
			}, nil
		},
		putObjectFunc: func(ctx context.Context, in *s3.PutObjectInput, opt ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			rec := putCallRecord{
				bucket: *in.Bucket,
				key:    *in.Key,
			}
			if in.SSEKMSKeyId != nil {
				rec.kmsKeyID = *in.SSEKMSKeyId
			}
			putRecords = append(putRecords, rec)
			return &s3.PutObjectOutput{}, nil
		},
	}
	mockKMS := &mockKMSClient{
		decryptFunc: func(ctx context.Context, in *kms.DecryptInput, opt ...func(*kms.Options)) (*kms.DecryptOutput, error) {
			return &kms.DecryptOutput{Plaintext: key}, nil
		},
	}

	p := New(mockS3, mockKMS)

	complianceFilter := &filter.FilterConfig{
		Name: "compliance",
		Match: &filter.MatchConfig{
			Clusters: []string{"*"},
		},
		Output: &filter.OutputConfig{
			Bucket: "compliance-dedicated-bucket",
			Prefix: strPtr("audit"),
		},
	}

	secopsFilter := &filter.FilterConfig{
		Name: "secops",
		Match: &filter.MatchConfig{
			Clusters: []string{"*"},
		},
		Output: &filter.OutputConfig{
			Bucket:   "secops-vault-bucket",
			KmsKeyID: "arn:aws:kms:us-east-1:123456789012:key/secops-cmk",
			Prefix:   strPtr("secops"),
		},
	}

	p.SetFilterConfigs([]*filter.FilterConfig{complianceFilter, secopsFilter})

	firehoseKey := "firehose/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-events.json"
	s3NotifJSON := fmt.Sprintf(`{"Records":[{"s3":{"bucket":{"name":%q},"object":{"key":%q}}}]}`, "raw-source-bucket", firehoseKey)
	msg := events.SQSMessage{
		MessageId: "msg-per-filter-dest",
		Body:      s3NotifJSON,
	}

	err := p.ProcessMessage(ctx, msg, "")
	if err != nil {
		t.Fatalf("expected successful message processing, got: %v", err)
	}

	if len(putRecords) != 2 {
		t.Fatalf("expected 2 PutObject calls, got %d", len(putRecords))
	}

	// Verify compliance output
	if putRecords[0].bucket != "compliance-dedicated-bucket" {
		t.Errorf("expected bucket[0]=compliance-dedicated-bucket, got %q", putRecords[0].bucket)
	}
	if putRecords[0].kmsKeyID != "" {
		t.Errorf("expected kmsKeyID[0] to be empty, got %q", putRecords[0].kmsKeyID)
	}

	// Verify secops output
	if putRecords[1].bucket != "secops-vault-bucket" {
		t.Errorf("expected bucket[1]=secops-vault-bucket, got %q", putRecords[1].bucket)
	}
	if putRecords[1].kmsKeyID != "arn:aws:kms:us-east-1:123456789012:key/secops-cmk" {
		t.Errorf("expected kmsKeyID[1]=arn:aws:kms:us-east-1:123456789012:key/secops-cmk, got %q", putRecords[1].kmsKeyID)
	}
}

func TestProcessor_EndToEnd_CrossRegionKMS(t *testing.T) {
	ctx := context.Background()

	key := []byte("01234567890123456789012345678901")
	sampleEvents := `{"type":"DatabaseActivityMonitoringRecords","version":"1.0","databaseActivityEventList":[{"command":"SELECT 1"}]}`
	var b bytes.Buffer
	zw := zlib.NewWriter(&b)
	zw.Write([]byte(sampleEvents))
	zw.Close()

	matProv, _ := mpl.NewClient(mpltypes.MaterialProvidersConfig{})
	aesKeyring, _ := matProv.CreateRawAesKeyring(ctx, mpltypes.CreateRawAesKeyringInput{
		KeyName:      "DataKey",
		KeyNamespace: "RawMasterKeyProvider",
		WrappingKey:  key,
		WrappingAlg:  mpltypes.AesWrappingAlgAlgAes256GcmIv12Tag16,
	})
	encClient, _ := client.NewClient(esdktypes.AwsEncryptionSdkConfig{})
	encRes, _ := encClient.Encrypt(ctx, esdktypes.EncryptInput{
		Plaintext: b.Bytes(),
		Keyring:   aesKeyring,
	})

	dasPayload := model.DASPayload{
		Type:                   "DatabaseActivityMonitoringRecords",
		Version:                "1.0",
		Key:                    base64.StdEncoding.EncodeToString([]byte("mock-kms-key")),
		DatabaseActivityEvents: base64.StdEncoding.EncodeToString(encRes.Ciphertext),
	}
	dasPayloadBytes, _ := json.Marshal(dasPayload)

	cases := []struct {
		name           string
		s3Key          string
		filterRegion   string
		envRegion      string
		expectedRegion string
	}{
		{
			name:           "Per-filter kms_region takes priority over environment variable",
			s3Key:          "firehose/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-events.json",
			filterRegion:   "eu-west-1",
			envRegion:      "ap-northeast-1",
			expectedRegion: "eu-west-1",
		},
		{
			name:           "Environment DAS_KMS_REGION_NAME fallback when filter has no kms_region",
			s3Key:          "firehose/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-events.json",
			filterRegion:   "",
			envRegion:      "ca-central-1",
			expectedRegion: "ca-central-1",
		},
		{
			name:           "Ambient default empty when no filter kms_region and no env var",
			s3Key:          "firehose/cluster-EXAMPLE1234567890ABCDEF/2026/10/01/05/das-events.json",
			filterRegion:   "",
			envRegion:      "",
			expectedRegion: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DAS_KMS_REGION_NAME", tc.envRegion)

			var capturedRegion string
			mockS3 := &mockS3Client{
				getObjectFunc: func(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
					return &s3.GetObjectOutput{
						Body: io.NopCloser(bytes.NewReader(dasPayloadBytes)),
					}, nil
				},
				putObjectFunc: func(ctx context.Context, in *s3.PutObjectInput, opt ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
					return &s3.PutObjectOutput{}, nil
				},
			}
			mockKMS := &mockKMSClient{
				decryptFunc: func(ctx context.Context, in *kms.DecryptInput, opt ...func(*kms.Options)) (*kms.DecryptOutput, error) {
					var opts kms.Options
					for _, fn := range opt {
						fn(&opts)
					}
					capturedRegion = opts.Region
					return &kms.DecryptOutput{Plaintext: key}, nil
				},
			}

			p := New(mockS3, mockKMS)
			filterCfg := &filter.FilterConfig{
				Name:      "test-filter",
				KMSRegion: tc.filterRegion,
				Match: &filter.MatchConfig{
					Clusters: []string{"*"},
				},
			}
			p.SetFilterConfigs([]*filter.FilterConfig{filterCfg})

			s3NotifJSON := fmt.Sprintf(`{"Records":[{"s3":{"bucket":{"name":%q},"object":{"key":%q}}}]}`, "raw-source-bucket", tc.s3Key)
			msg := events.SQSMessage{
				MessageId: "msg-kms-region-test",
				Body:      s3NotifJSON,
			}

			err := p.ProcessMessage(ctx, msg, "")
			if err != nil {
				t.Fatalf("unexpected ProcessMessage error: %v", err)
			}

			if capturedRegion != tc.expectedRegion {
				t.Errorf("expected KMS Decrypt region %q, got %q", tc.expectedRegion, capturedRegion)
			}
		})
	}
}

func TestProcessor_EndToEnd_IgnoredErrorPrefix(t *testing.T) {
	ctx := context.Background()

	getObjectCalls := 0
	kmsDecryptCalls := 0

	mockS3 := &mockS3Client{
		getObjectFunc: func(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			getObjectCalls++
			return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader([]byte("{}")))}, nil
		},
		putObjectFunc: func(ctx context.Context, in *s3.PutObjectInput, opt ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			return &s3.PutObjectOutput{}, nil
		},
	}
	mockKMS := &mockKMSClient{
		decryptFunc: func(ctx context.Context, in *kms.DecryptInput, opt ...func(*kms.Options)) (*kms.DecryptOutput, error) {
			kmsDecryptCalls++
			return &kms.DecryptOutput{Plaintext: []byte("01234567890123456789012345678901")}, nil
		},
	}

	p := New(mockS3, mockKMS)
	p.SetFilterConfigs([]*filter.FilterConfig{{
		Name: "all",
		Match: &filter.MatchConfig{
			Clusters: []string{"*"},
		},
	}})

	errorKey := "AWSLogs/123456789012/DAS/us-east-1/cluster-EXAMPLE1234567890ABCDEF/errors/2026/10/10/ProcessingFailed-123.json"
	s3NotifJSON := fmt.Sprintf(`{"Records":[{"s3":{"bucket":{"name":%q},"object":{"key":%q}}}]}`, "raw-source-bucket", errorKey)
	msg := events.SQSMessage{
		MessageId: "msg-firehose-error",
		Body:      s3NotifJSON,
	}

	err := p.ProcessMessage(ctx, msg, "")
	if err != nil {
		t.Fatalf("expected nil error for ignored error prefix, got: %v", err)
	}

	if getObjectCalls != 0 {
		t.Errorf("expected 0 GetObject calls for ignored error key, got %d", getObjectCalls)
	}
	if kmsDecryptCalls != 0 {
		t.Errorf("expected 0 KMS Decrypt calls for ignored error key, got %d", kmsDecryptCalls)
	}
}

func TestProcessor_EndToEnd_EventBridgeS3Notification(t *testing.T) {
	ctx := context.Background()

	key := []byte("01234567890123456789012345678901")
	sampleEvents := `{"type":"DatabaseActivityMonitoringRecords","version":"1.0","databaseActivityEventList":[{"command":"SELECT 1"}]}`
	var b bytes.Buffer
	zw := zlib.NewWriter(&b)
	zw.Write([]byte(sampleEvents))
	zw.Close()

	matProv, _ := mpl.NewClient(mpltypes.MaterialProvidersConfig{})
	aesKeyring, _ := matProv.CreateRawAesKeyring(ctx, mpltypes.CreateRawAesKeyringInput{
		KeyName:      "DataKey",
		KeyNamespace: "RawMasterKeyProvider",
		WrappingKey:  key,
		WrappingAlg:  mpltypes.AesWrappingAlgAlgAes256GcmIv12Tag16,
	})
	encClient, _ := client.NewClient(esdktypes.AwsEncryptionSdkConfig{})
	encRes, _ := encClient.Encrypt(ctx, esdktypes.EncryptInput{
		Plaintext: b.Bytes(),
		Keyring:   aesKeyring,
	})

	dasPayload := model.DASPayload{
		Type:                   "DatabaseActivityMonitoringRecords",
		Version:                "1.0",
		Key:                    base64.StdEncoding.EncodeToString([]byte("mock-kms-key")),
		DatabaseActivityEvents: base64.StdEncoding.EncodeToString(encRes.Ciphertext),
	}
	dasPayloadBytes, _ := json.Marshal(dasPayload)

	var writtenBucket string
	var writtenKey string

	mockS3 := &mockS3Client{
		getObjectFunc: func(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(bytes.NewReader(dasPayloadBytes)),
			}, nil
		},
		putObjectFunc: func(ctx context.Context, in *s3.PutObjectInput, opt ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			writtenBucket = *in.Bucket
			writtenKey = *in.Key
			return &s3.PutObjectOutput{}, nil
		},
	}
	mockKMS := &mockKMSClient{
		decryptFunc: func(ctx context.Context, in *kms.DecryptInput, opt ...func(*kms.Options)) (*kms.DecryptOutput, error) {
			return &kms.DecryptOutput{Plaintext: key}, nil
		},
	}

	p := New(mockS3, mockKMS)
	filterCfg := &filter.FilterConfig{
		Name: "compliance",
		Match: &filter.MatchConfig{
			Clusters: []string{"*"},
		},
		Output: &filter.OutputConfig{
			Bucket: "analytics-destination-bucket",
			Format: "hive",
			Prefix: strPtr("audit"),
		},
	}
	p.SetFilterConfigs([]*filter.FilterConfig{filterCfg})

	ebEventJSON := `{
		"version": "0",
		"id": "17793124-05d5-b190-f004-9f1244104aa4",
		"detail-type": "Object Created",
		"source": "aws.s3",
		"account": "123456789012",
		"time": "2026-10-12T00:37:34Z",
		"region": "us-east-1",
		"resources": ["arn:aws:s3:::raw-source-bucket"],
		"detail": {
			"version": "0",
			"bucket": {"name": "raw-source-bucket"},
			"object": {"key": "AWSLogs/123456789012/DAS/us-east-1/cluster-EXAMPLE1234567890ABCDEF/2026/10/12/das-file.json"}
		}
	}`

	msg := events.SQSMessage{
		MessageId: "msg-eventbridge-e2e",
		Body:      ebEventJSON,
	}

	err := p.ProcessMessage(ctx, msg, "")
	if err != nil {
		t.Fatalf("unexpected error processing EventBridge S3 notification: %v", err)
	}

	if writtenBucket != "analytics-destination-bucket" {
		t.Errorf("expected writtenBucket=analytics-destination-bucket, got %q", writtenBucket)
	}
	if !strings.HasPrefix(writtenKey, "audit/cluster=cluster-EXAMPLE1234567890ABCDEF/") {
		t.Errorf("expected key to start with audit/cluster=..., got %q", writtenKey)
	}
}

func TestProcessor_EndToEnd_MultipleInputS3Buckets(t *testing.T) {
	ctx := context.Background()

	key := []byte("01234567890123456789012345678901")
	sampleEvents := `{"type":"DatabaseActivityMonitoringRecords","version":"1.0","databaseActivityEventList":[{"command":"SELECT 1"}]}`
	var b bytes.Buffer
	zw := zlib.NewWriter(&b)
	zw.Write([]byte(sampleEvents))
	zw.Close()

	matProv, _ := mpl.NewClient(mpltypes.MaterialProvidersConfig{})
	aesKeyring, _ := matProv.CreateRawAesKeyring(ctx, mpltypes.CreateRawAesKeyringInput{
		KeyName:      "DataKey",
		KeyNamespace: "RawMasterKeyProvider",
		WrappingKey:  key,
		WrappingAlg:  mpltypes.AesWrappingAlgAlgAes256GcmIv12Tag16,
	})
	encClient, _ := client.NewClient(esdktypes.AwsEncryptionSdkConfig{})
	encRes, _ := encClient.Encrypt(ctx, esdktypes.EncryptInput{
		Plaintext: b.Bytes(),
		Keyring:   aesKeyring,
	})

	dasPayload := model.DASPayload{
		Type:                   "DatabaseActivityMonitoringRecords",
		Version:                "1.0",
		Key:                    base64.StdEncoding.EncodeToString([]byte("mock-kms-key")),
		DatabaseActivityEvents: base64.StdEncoding.EncodeToString(encRes.Ciphertext),
	}
	dasPayloadBytes, _ := json.Marshal(dasPayload)

	type writeOp struct {
		bucket string
		key    string
	}
	var writes []writeOp

	mockS3 := &mockS3Client{
		getObjectFunc: func(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(bytes.NewReader(dasPayloadBytes)),
			}, nil
		},
		putObjectFunc: func(ctx context.Context, in *s3.PutObjectInput, opt ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			writes = append(writes, writeOp{bucket: *in.Bucket, key: *in.Key})
			return &s3.PutObjectOutput{}, nil
		},
	}
	mockKMS := &mockKMSClient{
		decryptFunc: func(ctx context.Context, in *kms.DecryptInput, opt ...func(*kms.Options)) (*kms.DecryptOutput, error) {
			return &kms.DecryptOutput{Plaintext: key}, nil
		},
	}

	p := New(mockS3, mockKMS)
	filterA := &filter.FilterConfig{
		Name: "bucket-a-filter",
		Match: &filter.MatchConfig{
			Buckets: []string{"raw-bucket-prod-a"},
		},
		Output: &filter.OutputConfig{
			Bucket: "dest-bucket-a",
			Format: "hive",
			Prefix: strPtr("audit-a"),
		},
	}
	filterB := &filter.FilterConfig{
		Name: "bucket-b-filter",
		Match: &filter.MatchConfig{
			Buckets: []string{"raw-bucket-prod-b"},
		},
		Output: &filter.OutputConfig{
			Bucket: "dest-bucket-b",
			Format: "hive",
			Prefix: strPtr("audit-b"),
		},
	}
	p.SetFilterConfigs([]*filter.FilterConfig{filterA, filterB})

	// 1. Process EventBridge message from raw-bucket-prod-a
	msgA := events.SQSMessage{
		MessageId: "msg-bucket-a",
		Body: `{
			"source": "aws.s3",
			"detail-type": "Object Created",
			"detail": {
				"bucket": {"name": "raw-bucket-prod-a"},
				"object": {"key": "AWSLogs/123456789012/DAS/us-east-1/cluster-EXAMPLE1234567890ABCDEF/2026/10/12/das-a.json"}
			}
		}`,
	}
	if err := p.ProcessMessage(ctx, msgA, ""); err != nil {
		t.Fatalf("unexpected error processing msgA: %v", err)
	}

	// 2. Process EventBridge message from raw-bucket-prod-b
	msgB := events.SQSMessage{
		MessageId: "msg-bucket-b",
		Body: `{
			"source": "aws.s3",
			"detail-type": "Object Created",
			"detail": {
				"bucket": {"name": "raw-bucket-prod-b"},
				"object": {"key": "AWSLogs/123456789012/DAS/us-east-1/cluster-EXAMPLE1234567890ABCDEF/2026/10/12/das-b.json"}
			}
		}`,
	}
	if err := p.ProcessMessage(ctx, msgB, ""); err != nil {
		t.Fatalf("unexpected error processing msgB: %v", err)
	}

	// 3. Process EventBridge message from unmatched bucket (should be skipped)
	msgOther := events.SQSMessage{
		MessageId: "msg-bucket-other",
		Body: `{
			"source": "aws.s3",
			"detail-type": "Object Created",
			"detail": {
				"bucket": {"name": "raw-bucket-other"},
				"object": {"key": "AWSLogs/123456789012/DAS/us-east-1/cluster-EXAMPLE1234567890ABCDEF/2026/10/12/das-other.json"}
			}
		}`,
	}
	if err := p.ProcessMessage(ctx, msgOther, ""); err != nil {
		t.Fatalf("unexpected error processing msgOther: %v", err)
	}

	// Check writes: should only have 2 writes (one to dest-bucket-a and one to dest-bucket-b)
	if len(writes) != 2 {
		t.Fatalf("expected exactly 2 writes, got %d", len(writes))
	}
	if writes[0].bucket != "dest-bucket-a" {
		t.Errorf("expected write 0 bucket=dest-bucket-a, got %q", writes[0].bucket)
	}
	if !strings.HasPrefix(writes[0].key, "audit-a/") {
		t.Errorf("expected write 0 prefix=audit-a/, got %q", writes[0].key)
	}
	if writes[1].bucket != "dest-bucket-b" {
		t.Errorf("expected write 1 bucket=dest-bucket-b, got %q", writes[1].bucket)
	}
	if !strings.HasPrefix(writes[1].key, "audit-b/") {
		t.Errorf("expected write 1 prefix=audit-b/, got %q", writes[1].key)
	}
}

func TestProcessor_EndToEnd_S3TestEvent(t *testing.T) {
	ctx := context.Background()

	getObjectCalls := 0
	kmsDecryptCalls := 0

	mockS3 := &mockS3Client{
		getObjectFunc: func(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			getObjectCalls++
			return &s3.GetObjectOutput{}, nil
		},
		putObjectFunc: func(ctx context.Context, in *s3.PutObjectInput, opt ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			return &s3.PutObjectOutput{}, nil
		},
	}
	mockKMS := &mockKMSClient{
		decryptFunc: func(ctx context.Context, in *kms.DecryptInput, opt ...func(*kms.Options)) (*kms.DecryptOutput, error) {
			kmsDecryptCalls++
			return &kms.DecryptOutput{}, nil
		},
	}

	p := New(mockS3, mockKMS)

	// 1. Direct s3:TestEvent
	directMsg := events.SQSMessage{
		MessageId: "msg-direct-test",
		Body: `{
			"Service": "Amazon S3",
			"Event": "s3:TestEvent",
			"Time": "2026-10-12T00:00:00.000Z",
			"Bucket": "my-test-bucket",
			"RequestId": "REQ123",
			"HostId": "HOST456"
		}`,
	}
	if err := p.ProcessMessage(ctx, directMsg, ""); err != nil {
		t.Fatalf("expected nil error for direct s3:TestEvent, got: %v", err)
	}

	// 2. SNS wrapped s3:TestEvent
	snsMsg := events.SQSMessage{
		MessageId: "msg-sns-test",
		Body: `{
			"Type": "Notification",
			"MessageId": "msg-123",
			"TopicArn": "arn:aws:sns:us-east-1:123456789012:das-topic",
			"Message": "{\"Service\":\"Amazon S3\",\"Event\":\"s3:TestEvent\",\"Bucket\":\"my-test-bucket\"}"
		}`,
	}
	if err := p.ProcessMessage(ctx, snsMsg, ""); err != nil {
		t.Fatalf("expected nil error for SNS-wrapped s3:TestEvent, got: %v", err)
	}

	if getObjectCalls != 0 {
		t.Errorf("expected 0 GetObject calls for test events, got %d", getObjectCalls)
	}
	if kmsDecryptCalls != 0 {
		t.Errorf("expected 0 KMS Decrypt calls for test events, got %d", kmsDecryptCalls)
	}
}
