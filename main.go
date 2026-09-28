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

	mpl "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygenerated"
	mpltypes "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygeneratedtypes"
	client "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygenerated"
	esdktypes "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygeneratedtypes"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/snappy"
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
	if filterName == "" {
		filterName = "default"
	}

	filterConfig, err := LoadFilterConfig(filterName)
	if err != nil {
		return fmt.Errorf("failed to load filter config: %w", err)
	}

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

			decryptedPayload, err := decryptAWSEncryptionSDKPayload(ctx, decodedPayload, plaintextDataKey)
			if err != nil {
				return fmt.Errorf("failed to decrypt events payload: %w", err)
			}

			// 5. Decompress the payload
			decompressedPayload, err := decompressZlib(decryptedPayload)
			if err != nil {
				return fmt.Errorf("failed to decompress events payload: %w", err)
			}

			// 6. Convert to Parquet
			parquetBytes, err := convertJSONToParquet(decompressedPayload, filterConfig)
			if err != nil {
				return fmt.Errorf("failed to convert JSON to Parquet: %w", err)
			}

			// 7. Write to destination S3 (Fanout)
			destKey := fmt.Sprintf("das/%s/%s-processed.parquet", filterName, key)
			slog.Info("Writing processed data back to S3", "DestBucket", bucket, "DestKey", destKey)

			_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(destKey),
				Body:   bytes.NewReader(parquetBytes),
			})
			if err != nil {
				return fmt.Errorf("failed to write processed data: %w", err)
			}
		}
	}

	return nil
}

// decryptAWSEncryptionSDKPayload uses the plaintext data key to decrypt the AWS Encryption SDK message format.
func decryptAWSEncryptionSDKPayload(ctx context.Context, ciphertext, plaintextDataKey []byte) ([]byte, error) {
	matProv, err := mpl.NewClient(mpltypes.MaterialProvidersConfig{})
	if err != nil {
		return nil, fmt.Errorf("failed to create material providers client: %w", err)
	}

	// The keyNamespace and keyName must match what was used during encryption (in the Python RawMasterKeyProvider)
	aesKeyRingInput := mpltypes.CreateRawAesKeyringInput{
		KeyName:      "DataKey",
		KeyNamespace: "RawMasterKeyProvider",
		WrappingKey:  plaintextDataKey,
		WrappingAlg:  mpltypes.AesWrappingAlgAlgAes256GcmIv12Tag16,
	}
	aesKeyring, err := matProv.CreateRawAesKeyring(ctx, aesKeyRingInput)
	if err != nil {
		return nil, fmt.Errorf("failed to create raw aes keyring: %w", err)
	}

	encryptionClient, err := client.NewClient(esdktypes.AwsEncryptionSdkConfig{})
	if err != nil {
		return nil, fmt.Errorf("failed to create encryption sdk client: %w", err)
	}

	decryptOutput, err := encryptionClient.Decrypt(ctx, esdktypes.DecryptInput{
		Ciphertext: ciphertext,
		Keyring:    aesKeyring,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt payload: %w", err)
	}

	return decryptOutput.Plaintext, nil
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

// DatabaseActivityEvent represents a single DAS event
type DatabaseActivityEvent struct {
	LogTime           string   `parquet:"logTime,dict,plain" json:"logTime"`
	StatementId       int64    `parquet:"statementId" json:"statementId"`
	SubstatementId    int64    `parquet:"substatementId" json:"substatementId"`
	ObjectType        string   `parquet:"objectType,dict,plain" json:"objectType"`
	Command           string   `parquet:"command,dict,plain" json:"command"`
	ObjectName        string   `parquet:"objectName,dict,plain" json:"objectName"`
	DatabaseName      string   `parquet:"databaseName,dict,plain" json:"databaseName"`
	DbUserName        string   `parquet:"dbUserName,dict,plain" json:"dbUserName"`
	RemoteHost        string   `parquet:"remoteHost,dict,plain" json:"remoteHost"`
	SessionId         string   `parquet:"sessionId,dict,plain" json:"sessionId"`
	RowCount          int64    `parquet:"rowCount" json:"rowCount"`
	CommandText       string   `parquet:"commandText,dict,plain" json:"commandText"`
	ParamList         []string `parquet:"paramList,list" json:"paramList"`
	Pid               int64    `parquet:"pid" json:"pid"`
	ClientApplication string   `parquet:"clientApplication,dict,plain" json:"clientApplication"`
	ExitCode          string   `parquet:"exitCode,dict,plain" json:"exitCode"`
	Class             string   `parquet:"class,dict,plain" json:"class"`
	ServerHost        string   `parquet:"serverHost,dict,plain" json:"serverHost"`
	Type              string   `parquet:"type,dict,plain" json:"type"`
	StartTime         string   `parquet:"startTime,dict,plain" json:"startTime"`
	ErrorMessage      string   `parquet:"errorMessage,dict,plain" json:"errorMessage"`
}

func convertJSONToParquet(decompressedJSON []byte, filterConfig *FilterConfig) ([]byte, error) {
	// We unmarshal into a generic map to perform filtering and drops
	var container struct {
		DatabaseActivityEventList []map[string]interface{} `json:"databaseActivityEventList"`
	}
	if err := json.Unmarshal(decompressedJSON, &container); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON events: %w", err)
	}

	var buf bytes.Buffer
	writer := parquet.NewWriter(&buf, parquet.Compression(&snappy.Codec{}))

	for _, eventMap := range container.DatabaseActivityEventList {
		if t, ok := eventMap["type"].(string); ok && t == "heartbeat" {
			continue
		}

		if exitCode, ok := eventMap["exitCode"]; ok {
			eventMap["exitCode"] = fmt.Sprintf("%v", exitCode)
		}

		if EvaluateFilter(eventMap, filterConfig.Query) {
			for _, d := range filterConfig.Drop {
				delete(eventMap, d)
			}

			// Convert map back to bytes then struct to leverage strict typing for Parquet schema
			eventBytes, err := json.Marshal(eventMap)
			if err != nil {
				continue
			}

			var event DatabaseActivityEvent
			if err := json.Unmarshal(eventBytes, &event); err == nil {
				if err := writer.Write(event); err != nil {
					return nil, fmt.Errorf("failed to write parquet row: %w", err)
				}
			}
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close parquet writer: %w", err)
	}

	return buf.Bytes(), nil
}

func main() {
	slog.Info("Starting AWS DAS Processor Lambda...")
	lambda.Start(handler)
}
