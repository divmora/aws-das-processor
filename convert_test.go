package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"os"
	"testing"

	mpl "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygenerated"
	mpltypes "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygeneratedtypes"
	client "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygenerated"
	esdktypes "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygeneratedtypes"
	"github.com/parquet-go/parquet-go"
)

// Scenario 1: Heartbeat filtering — heartbeat events should be skipped
func TestConvertJSONToParquet_HeartbeatFiltering(t *testing.T) {
	rawJSON := []byte(`{
		"databaseActivityEventList": [
			{"type": "heartbeat"},
			{"type": "heartbeat"},
			{"type": "activity", "command": "SELECT", "dbUserName": "alice"}
		]
	}`)

	filterConfig := &FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}
	parquetBytes, err := convertJSONToParquet(rawJSON, filterConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reader := parquet.NewReader(bytes.NewReader(parquetBytes))
	var rows []DatabaseActivityEvent
	for {
		var row DatabaseActivityEvent
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

	filterConfig := &FilterConfig{
		Drop:  []string{"errorMessage", "command"},
		Query: map[string]interface{}{},
	}
	parquetBytes, err := convertJSONToParquet(rawJSON, filterConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reader := parquet.NewReader(bytes.NewReader(parquetBytes))
	var row DatabaseActivityEvent
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

	filterConfig := &FilterConfig{
		Drop: []string{},
		Query: map[string]interface{}{
			"type":      "selector",
			"dimension": "command",
			"value":     "DROP",
		},
	}
	parquetBytes, err := convertJSONToParquet(rawJSON, filterConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reader := parquet.NewReader(bytes.NewReader(parquetBytes))
	var rows []DatabaseActivityEvent
	for {
		var row DatabaseActivityEvent
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
	filterConfig := &FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}
	_, err := convertJSONToParquet([]byte("{not-valid-json"), filterConfig)
	if err == nil {
		t.Fatal("expected error on malformed JSON, got nil")
	}
}

// Scenario 5: Empty event list — returns valid parquet with 0 rows
func TestConvertJSONToParquet_EmptyEventList(t *testing.T) {
	rawJSON := []byte(`{"databaseActivityEventList": []}`)
	filterConfig := &FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}

	parquetBytes, err := convertJSONToParquet(rawJSON, filterConfig)
	if err != nil {
		t.Fatalf("unexpected error on empty event list: %v", err)
	}

	reader := parquet.NewReader(bytes.NewReader(parquetBytes))
	var rows []DatabaseActivityEvent
	for {
		var row DatabaseActivityEvent
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

	filterConfig := &FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}
	parquetBytes, err := convertJSONToParquet(rawJSON, filterConfig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reader := parquet.NewReader(bytes.NewReader(parquetBytes))
	var rows []DatabaseActivityEvent
	for {
		var row DatabaseActivityEvent
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

// TestDecompressZlib_Valid tests round-trip zlib compression and decompression.
func TestDecompressZlib_Valid(t *testing.T) {
	originalData := []byte("The quick brown fox jumps over the lazy dog Database Activity Stream")

	var buf bytes.Buffer
	writer := zlib.NewWriter(&buf)
	_, err := writer.Write(originalData)
	if err != nil {
		t.Fatalf("failed to write zlib: %v", err)
	}
	_ = writer.Close()

	decompressed, err := decompressZlib(buf.Bytes())
	if err != nil {
		t.Fatalf("failed to decompress: %v", err)
	}

	if string(decompressed) != string(originalData) {
		t.Errorf("expected %q, got %q", string(originalData), string(decompressed))
	}
}

// TestDecompressZlib_InvalidInput tests that corrupted data returns error.
func TestDecompressZlib_InvalidInput(t *testing.T) {
	corrupted := []byte("not-a-valid-zlib-stream")
	_, err := decompressZlib(corrupted)
	if err == nil {
		t.Fatal("expected error on corrupted zlib stream, got nil")
	}
}

// TestDecompressZlib_EmptyInput tests that empty input returns error.
func TestDecompressZlib_EmptyInput(t *testing.T) {
	_, err := decompressZlib([]byte{})
	if err == nil {
		t.Fatal("expected error on empty zlib stream, got nil")
	}
}

// TestDecryptAWSEncryptionSDKPayload tests the ESDK decryption path with valid and invalid inputs.
func TestDecryptAWSEncryptionSDKPayload(t *testing.T) {
	ctx := context.Background()
	rawKey := []byte("01234567890123456789012345678901") // 32-byte key
	plaintext := []byte("Hello, AWS Encryption SDK DAS Payload!")

	// 1. Encrypt payload
	matProv, err := mpl.NewClient(mpltypes.MaterialProvidersConfig{})
	if err != nil {
		t.Fatalf("failed to create mpl client: %v", err)
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
		Plaintext: plaintext,
		Keyring:   aesKeyring,
	})
	if err != nil {
		t.Fatalf("failed to encrypt: %v", err)
	}

	// 2. Decrypt with correct key
	decrypted, err := decryptAWSEncryptionSDKPayload(ctx, encOutput.Ciphertext, rawKey)
	if err != nil {
		t.Fatalf("failed to decrypt valid ciphertext: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("expected %q, got %q", string(plaintext), string(decrypted))
	}

	// 3. Decrypt with wrong key fails
	wrongKey := []byte("11111111111111111111111111111111")
	_, err = decryptAWSEncryptionSDKPayload(ctx, encOutput.Ciphertext, wrongKey)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key, got nil")
	}

	// 4. Decrypt malformed ciphertext fails
	_, err = decryptAWSEncryptionSDKPayload(ctx, []byte("invalid-ciphertext"), rawKey)
	if err == nil {
		t.Fatal("expected error decrypting corrupted ciphertext, got nil")
	}
}

// TestFixtures_Unmarshal verifies that all fixtures in testdata/ parse properly.
func TestFixtures_Unmarshal(t *testing.T) {
	// S3 notification fixture
	s3Data, err := os.ReadFile("testdata/s3_event_notification.json")
	if err != nil {
		t.Fatalf("failed to read s3 fixture: %v", err)
	}
	var s3Notif S3EventNotification
	if err := json.Unmarshal(s3Data, &s3Notif); err != nil {
		t.Fatalf("failed to unmarshal s3 fixture: %v", err)
	}
	if len(s3Notif.Records) != 1 || s3Notif.Records[0].S3.Bucket.Name != "aws-das-source-bucket" {
		t.Errorf("unexpected s3 fixture content: %+v", s3Notif)
	}

	// SNS fixture
	snsData, err := os.ReadFile("testdata/sns_s3_event.json")
	if err != nil {
		t.Fatalf("failed to read sns fixture: %v", err)
	}
	var snsMsg struct {
		Type    string `json:"Type"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal(snsData, &snsMsg); err != nil {
		t.Fatalf("failed to unmarshal sns fixture: %v", err)
	}
	var innerS3 S3EventNotification
	if err := json.Unmarshal([]byte(snsMsg.Message), &innerS3); err != nil {
		t.Fatalf("failed to unmarshal inner S3 notification from SNS: %v", err)
	}
	if len(innerS3.Records) != 1 {
		t.Errorf("expected 1 record in inner S3 notification")
	}

	// DAS events fixture
	dasData, err := os.ReadFile("testdata/das_events.json")
	if err != nil {
		t.Fatalf("failed to read das_events fixture: %v", err)
	}
	filterCfg := &FilterConfig{Drop: []string{}, Query: map[string]interface{}{}}
	parquetBytes, err := convertJSONToParquet(dasData, filterCfg)
	if err != nil {
		t.Fatalf("failed to convert das_events fixture to parquet: %v", err)
	}
	if len(parquetBytes) == 0 {
		t.Errorf("expected non-empty parquet output for das_events fixture")
	}

	// Heartbeat-only fixture
	hbData, err := os.ReadFile("testdata/das_heartbeat_only.json")
	if err != nil {
		t.Fatalf("failed to read heartbeat fixture: %v", err)
	}
	hbBytes, err := convertJSONToParquet(hbData, filterCfg)
	if err != nil {
		t.Fatalf("failed to convert heartbeat fixture: %v", err)
	}
	if len(hbBytes) == 0 {
		t.Errorf("expected non-empty parquet schema header for heartbeat fixture")
	}

	// Empty fixture
	emptyData, err := os.ReadFile("testdata/das_empty.json")
	if err != nil {
		t.Fatalf("failed to read empty fixture: %v", err)
	}
	emptyBytes, err := convertJSONToParquet(emptyData, filterCfg)
	if err != nil {
		t.Fatalf("failed to convert empty fixture: %v", err)
	}
	if len(emptyBytes) == 0 {
		t.Errorf("expected non-empty parquet schema header for empty fixture")
	}
}
