package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	mpl "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygenerated"
	mpltypes "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygeneratedtypes"
	client "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygenerated"
	esdktypes "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygeneratedtypes"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// mockS3Client implements S3Client for unit testing.
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

// mockKMSClient implements KMSClient for unit testing.
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

// TestProcessSQSEvent_PartialBatchFailure verifies that when a batch contains both
// succeeding and failing records, only the failing record's ID is included in BatchItemFailures.
func TestProcessSQSEvent_PartialBatchFailure(t *testing.T) {
	ctx := context.Background()

	sqsEvent := events.SQSEvent{
		Records: []events.SQSMessage{
			{
				MessageId: "msg-success-1",
				Body:      `{"Records":[]}`,
			},
			{
				MessageId: "msg-failure-2",
				Body:      `invalid-payload`,
			},
		},
	}

	response, err := processSQSEvent(ctx, sqsEvent, func(ctx context.Context, msg events.SQSMessage) error {
		if msg.MessageId == "msg-failure-2" {
			return errors.New("simulated processing failure")
		}
		return nil
	})

	if err != nil {
		t.Fatalf("expected nil error from processSQSEvent, got: %v", err)
	}

	if len(response.BatchItemFailures) != 1 {
		t.Fatalf("expected exactly 1 failure, got %d", len(response.BatchItemFailures))
	}

	if response.BatchItemFailures[0].ItemIdentifier != "msg-failure-2" {
		t.Errorf("expected failed itemIdentifier to be 'msg-failure-2', got: %s", response.BatchItemFailures[0].ItemIdentifier)
	}
}

// TestProcessSQSEvent_AllSuccess verifies that when all records succeed, BatchItemFailures is empty.
func TestProcessSQSEvent_AllSuccess(t *testing.T) {
	ctx := context.Background()

	sqsEvent := events.SQSEvent{
		Records: []events.SQSMessage{
			{MessageId: "msg-1"},
			{MessageId: "msg-2"},
		},
	}

	response, err := processSQSEvent(ctx, sqsEvent, func(ctx context.Context, msg events.SQSMessage) error {
		return nil
	})

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if len(response.BatchItemFailures) != 0 {
		t.Errorf("expected 0 failures, got %d", len(response.BatchItemFailures))
	}
}

// TestProcessSQSEvent_AllFail verifies that when all records fail, all message IDs are returned.
func TestProcessSQSEvent_AllFail(t *testing.T) {
	ctx := context.Background()

	sqsEvent := events.SQSEvent{
		Records: []events.SQSMessage{
			{MessageId: "msg-1"},
			{MessageId: "msg-2"},
		},
	}

	response, err := processSQSEvent(ctx, sqsEvent, func(ctx context.Context, msg events.SQSMessage) error {
		return fmt.Errorf("failed: %s", msg.MessageId)
	})

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if len(response.BatchItemFailures) != 2 {
		t.Fatalf("expected 2 failures, got %d", len(response.BatchItemFailures))
	}

	if response.BatchItemFailures[0].ItemIdentifier != "msg-1" || response.BatchItemFailures[1].ItemIdentifier != "msg-2" {
		t.Errorf("unexpected failure identifiers: %+v", response.BatchItemFailures)
	}
}

// TestProcessSQSEvent_EmptyBatch verifies handling of empty event batch.
func TestProcessSQSEvent_EmptyBatch(t *testing.T) {
	ctx := context.Background()
	sqsEvent := events.SQSEvent{Records: []events.SQSMessage{}}

	response, err := processSQSEvent(ctx, sqsEvent, func(ctx context.Context, msg events.SQSMessage) error {
		return nil
	})

	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if len(response.BatchItemFailures) != 0 {
		t.Errorf("expected 0 failures, got %d", len(response.BatchItemFailures))
	}
}

// TestProcessMessage_InvalidJSON verifies that invalid message JSON returns a clear error.
func TestProcessMessage_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	filterCfg := &FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}

	msg := events.SQSMessage{
		MessageId: "test-msg-bad-json",
		Body:      "{bad json",
	}

	err := processMessage(ctx, msg, filterCfg, "default", "db-123")
	if err == nil {
		t.Fatal("expected error for invalid JSON body, got nil")
	}
}

// TestProcessMessage_InvalidSNSEnvelope verifies that malformed inner SNS message returns error.
func TestProcessMessage_InvalidSNSEnvelope(t *testing.T) {
	ctx := context.Background()
	filterCfg := &FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}

	msg := events.SQSMessage{
		MessageId: "test-msg-sns",
		Body:      `{"Type": "Notification", "Message": "malformed inner"}`,
	}

	err := processMessage(ctx, msg, filterCfg, "default", "db-123")
	if err == nil {
		t.Fatal("expected error for invalid SNS inner JSON, got nil")
	}
}

// TestProcessMessage_S3GetObjectError verifies that S3 fetch errors are wrapped and returned.
func TestProcessMessage_S3GetObjectError(t *testing.T) {
	ctx := context.Background()
	oldS3 := s3Client
	defer func() { s3Client = oldS3 }()

	s3Client = &mockS3Client{
		getObjectFunc: func(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return nil, errors.New("s3 connection reset")
		},
	}

	s3Notification := S3EventNotification{
		Records: []struct {
			S3 struct {
				Bucket struct {
					Name string `json:"name"`
				} `json:"bucket"`
				Object struct {
					Key string `json:"key"`
				} `json:"object"`
			} `json:"s3"`
		}{
			{
				S3: struct {
					Bucket struct {
						Name string `json:"name"`
					} `json:"bucket"`
					Object struct {
						Key string `json:"key"`
					} `json:"object"`
				}{
					Bucket: struct {
						Name string `json:"name"`
					}{Name: "test-bucket"},
					Object: struct {
						Key string `json:"key"`
					}{Key: "test-key"},
				},
			},
		},
	}
	bodyBytes, _ := json.Marshal(s3Notification)

	msg := events.SQSMessage{
		MessageId: "msg-s3-fail",
		Body:      string(bodyBytes),
	}

	filterCfg := &FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}
	err := processMessage(ctx, msg, filterCfg, "default", "db-123")
	if err == nil {
		t.Fatal("expected error when S3 GetObject fails, got nil")
	}
}

// TestProcessMessage_KMSDecryptError verifies that KMS decryption errors are wrapped and returned.
func TestProcessMessage_KMSDecryptError(t *testing.T) {
	ctx := context.Background()
	oldS3, oldKMS := s3Client, kmsClient
	defer func() {
		s3Client = oldS3
		kmsClient = oldKMS
	}()

	dasPayload := DASPayload{
		Type:                   "DatabaseActivityMonitoringRecord",
		Version:                "1.0",
		DatabaseActivityEvents: base64.StdEncoding.EncodeToString([]byte("dummy-events")),
		Key:                    base64.StdEncoding.EncodeToString([]byte("dummy-key")),
	}
	dasPayloadBytes, _ := json.Marshal(dasPayload)

	s3Client = &mockS3Client{
		getObjectFunc: func(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(bytes.NewReader(dasPayloadBytes)),
			}, nil
		},
	}

	kmsClient = &mockKMSClient{
		decryptFunc: func(ctx context.Context, in *kms.DecryptInput, opt ...func(*kms.Options)) (*kms.DecryptOutput, error) {
			return nil, errors.New("kms: AccessDeniedException")
		},
	}

	s3Notification := S3EventNotification{
		Records: []struct {
			S3 struct {
				Bucket struct {
					Name string `json:"name"`
				} `json:"bucket"`
				Object struct {
					Key string `json:"key"`
				} `json:"object"`
			} `json:"s3"`
		}{
			{
				S3: struct {
					Bucket struct {
						Name string `json:"name"`
					} `json:"bucket"`
					Object struct {
						Key string `json:"key"`
					} `json:"object"`
				}{
					Bucket: struct {
						Name string `json:"name"`
					}{Name: "test-bucket"},
					Object: struct {
						Key string `json:"key"`
					}{Key: "test-key"},
				},
			},
		},
	}
	s3NotifBytes, _ := json.Marshal(s3Notification)

	msg := events.SQSMessage{
		MessageId: "msg-kms-fail",
		Body:      string(s3NotifBytes),
	}

	filterCfg := &FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}
	err := processMessage(ctx, msg, filterCfg, "default", "db-123")
	if err == nil {
		t.Fatal("expected error when KMS Decrypt fails, got nil")
	}
}

// TestEndToEndProcessMessageSuccess creates an encrypted and compressed DAS payload and verifies
// full successful processing and Parquet output generation.
func TestEndToEndProcessMessageSuccess(t *testing.T) {
	ctx := context.Background()
	oldS3, oldKMS := s3Client, kmsClient
	defer func() {
		s3Client = oldS3
		kmsClient = oldKMS
	}()

	// 1. Prepare raw JSON events
	rawEventsJSON := `{"databaseActivityEventList":[{"type":"activity","dbUserName":"testuser","command":"SELECT","commandText":"SELECT 1","rowCount":1}]}`

	// 2. Compress with zlib
	var zlibBuf bytes.Buffer
	zw := zlib.NewWriter(&zlibBuf)
	_, _ = zw.Write([]byte(rawEventsJSON))
	_ = zw.Close()

	// 3. Encrypt with AWS Encryption SDK using a 32-byte key
	rawKey := []byte("01234567890123456789012345678901")
	matProv, err := mpl.NewClient(mpltypes.MaterialProvidersConfig{})
	if err != nil {
		t.Fatalf("failed to create material providers: %v", err)
	}
	aesKeyring, err := matProv.CreateRawAesKeyring(ctx, mpltypes.CreateRawAesKeyringInput{
		KeyName:      "DataKey",
		KeyNamespace: "RawMasterKeyProvider",
		WrappingKey:  rawKey,
		WrappingAlg:  mpltypes.AesWrappingAlgAlgAes256GcmIv12Tag16,
	})
	if err != nil {
		t.Fatalf("failed to create keyring: %v", err)
	}

	esdkClient, err := client.NewClient(esdktypes.AwsEncryptionSdkConfig{})
	if err != nil {
		t.Fatalf("failed to create esdk client: %v", err)
	}

	encOutput, err := esdkClient.Encrypt(ctx, esdktypes.EncryptInput{
		Plaintext: zlibBuf.Bytes(),
		Keyring:   aesKeyring,
	})
	if err != nil {
		t.Fatalf("failed to encrypt test payload: %v", err)
	}

	// 4. Create DASPayload
	dasPayload := DASPayload{
		Type:                   "DatabaseActivityMonitoringRecord",
		Version:                "1.0",
		DatabaseActivityEvents: base64.StdEncoding.EncodeToString(encOutput.Ciphertext),
		Key:                    base64.StdEncoding.EncodeToString([]byte("encrypted-data-key")),
	}
	dasBytes, _ := json.Marshal(dasPayload)

	// 5. Mock S3 and KMS
	var writtenKey string
	var writtenBucket string
	var writtenBody []byte

	s3Client = &mockS3Client{
		getObjectFunc: func(ctx context.Context, in *s3.GetObjectInput, opt ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{
				Body: io.NopCloser(bytes.NewReader(dasBytes)),
			}, nil
		},
		putObjectFunc: func(ctx context.Context, in *s3.PutObjectInput, opt ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
			writtenBucket = *in.Bucket
			writtenKey = *in.Key
			writtenBody, _ = io.ReadAll(in.Body)
			return &s3.PutObjectOutput{}, nil
		},
	}

	kmsClient = &mockKMSClient{
		decryptFunc: func(ctx context.Context, in *kms.DecryptInput, opt ...func(*kms.Options)) (*kms.DecryptOutput, error) {
			return &kms.DecryptOutput{Plaintext: rawKey}, nil
		},
	}

	s3Notification := S3EventNotification{
		Records: []struct {
			S3 struct {
				Bucket struct {
					Name string `json:"name"`
				} `json:"bucket"`
				Object struct {
					Key string `json:"key"`
				} `json:"object"`
			} `json:"s3"`
		}{
			{
				S3: struct {
					Bucket struct {
						Name string `json:"name"`
					} `json:"bucket"`
					Object struct {
						Key string `json:"key"`
					} `json:"object"`
				}{
					Bucket: struct {
						Name string `json:"name"`
					}{Name: "source-bucket"},
					Object: struct {
						Key string `json:"key"`
					}{Key: "das-log-key"},
				},
			},
		},
	}
	s3NotifBytes, _ := json.Marshal(s3Notification)

	msg := events.SQSMessage{
		MessageId: "msg-success",
		Body:      string(s3NotifBytes),
	}

	filterCfg := &FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}
	err = processMessage(ctx, msg, filterCfg, "default", "db-123")
	if err != nil {
		t.Fatalf("expected successful message processing, got error: %v", err)
	}

	if writtenBucket != "source-bucket" {
		t.Errorf("expected writtenBucket to be 'source-bucket', got %s", writtenBucket)
	}

	expectedKey := "das/default/das-log-key-processed.parquet"
	if writtenKey != expectedKey {
		t.Errorf("expected writtenKey to be %s, got %s", expectedKey, writtenKey)
	}

	if len(writtenBody) == 0 {
		t.Errorf("expected non-empty written parquet bytes")
	}
}
