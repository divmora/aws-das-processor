package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3EventNotification represents the standard S3 object created event JSON
type S3EventNotification struct {
	Records []struct {
		S3 struct {
			Bucket struct {
				Name string `json:"name"`
			} `json:"bucket"`
			Object struct {
				Key string `json:"key"`
			} `json:"object"`
		} `json:"s3"`
	} `json:"Records"`
}

// DASPayload represents the schema inside the Firehose S3 object for DAS
type DASPayload struct {
	Type                   string `json:"type"`
	Version                string `json:"version"`
	DatabaseActivityEvents string `json:"databaseActivityEvents"`
	Key                    string `json:"key"`
}

var (
	s3Client  *s3.Client
	kmsClient *kms.Client
)

func init() {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("Unable to load AWS config", "error", err)
		os.Exit(1)
	}
	s3Client = s3.NewFromConfig(cfg)
	kmsClient = kms.NewFromConfig(cfg)
}

// handler is the main entry point for the Lambda function.
func handler(ctx context.Context, sqsEvent events.SQSEvent) error {
	filterName := os.Getenv("DAS_FILTER_NAME")
	rdsResourceID := os.Getenv("DAS_RDS_RESOURCE_ID")

	for _, message := range sqsEvent.Records {
		slog.Info("Processing SQS message", "MessageId", message.MessageId)

		var s3Event S3EventNotification
		if err := json.Unmarshal([]byte(message.Body), &s3Event); err != nil {
			slog.Error("Failed to parse S3 event", "error", err, "body", message.Body)
			continue
		}

		for _, record := range s3Event.Records {
			bucket := record.S3.Bucket.Name
			key := record.S3.Object.Key

			slog.Info("Fetching S3 object", "Bucket", bucket, "Key", key)

			// 1. Fetch object from S3
			getObjectOutput, err := s3Client.GetObject(ctx, &s3.GetObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(key),
			})
			if err != nil {
				return fmt.Errorf("failed to fetch object %s/%s: %w", bucket, key, err)
			}

			bodyBytes, err := io.ReadAll(getObjectOutput.Body)
			getObjectOutput.Body.Close()
			if err != nil {
				return fmt.Errorf("failed to read S3 object body: %w", err)
			}

			// 2. Parse the DAS Payload
			var payload DASPayload
			if err := json.Unmarshal(bodyBytes, &payload); err != nil {
				return fmt.Errorf("failed to parse DAS payload: %w", err)
			}

			// 3. Decrypt the KMS Data Key
			decodedKmsKey, err := base64.StdEncoding.DecodeString(payload.Key)
			if err != nil {
				return fmt.Errorf("failed to base64 decode KMS key: %w", err)
			}

			decryptOutput, err := kmsClient.Decrypt(ctx, &kms.DecryptInput{
				CiphertextBlob: decodedKmsKey,
				EncryptionContext: map[string]string{
					"aws:rds:dbc-id": rdsResourceID,
				},
			})
			if err != nil {
				return fmt.Errorf("failed to decrypt KMS data key: %w", err)
			}

			plaintextDataKey := decryptOutput.Plaintext

			// 4. Decrypt the database activity events payload
			decodedPayload, err := base64.StdEncoding.DecodeString(payload.DatabaseActivityEvents)
			if err != nil {
				return fmt.Errorf("failed to base64 decode events payload: %w", err)
			}

			decryptedPayload, err := decryptAWSEncryptionSDKPayload(decodedPayload, plaintextDataKey)
			if err != nil {
				return fmt.Errorf("failed to decrypt events payload: %w", err)
			}

			// 5. Decompress the payload
			decompressedPayload, err := decompressZlib(decryptedPayload)
			if err != nil {
				return fmt.Errorf("failed to decompress events payload: %w", err)
			}

			// 6. Write to destination S3 (Fanout)
			destKey := fmt.Sprintf("das/%s/%s-processed.json", filterName, key)
			slog.Info("Writing processed data back to S3", "DestBucket", bucket, "DestKey", destKey)

			_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(destKey),
				Body:   bytes.NewReader(decompressedPayload),
			})
			if err != nil {
				return fmt.Errorf("failed to write processed data: %w", err)
			}
		}
	}

	return nil
}

// decryptAWSEncryptionSDKPayload uses the plaintext data key to decrypt the AWS Encryption SDK message format.
func decryptAWSEncryptionSDKPayload(ciphertext, plaintextDataKey []byte) ([]byte, error) {
	// TODO: AWS does not provide an official native Go client for the AWS Encryption SDK message format.
	// We will need to either use a community port (like github.com/chainifynet/aws-encryption-sdk-go)
	// or manually implement the AES-256-GCM RawMasterKeyProvider decryption logic here.
	return nil, fmt.Errorf("AWS Encryption SDK decryption not yet implemented")
}

// decompressZlib decompresses zlib formatted data
func decompressZlib(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	var out bytes.Buffer
	if _, err := io.Copy(&out, reader); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func main() {
	slog.Info("Starting AWS DAS Processor Lambda...")
	lambda.Start(handler)
}
