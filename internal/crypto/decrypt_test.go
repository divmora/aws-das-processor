package crypto

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"strings"
	"testing"

	mpl "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygenerated"
	mpltypes "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygeneratedtypes"
	client "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygenerated"
	esdktypes "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygeneratedtypes"
)

// TestDecompressPayload_Gzip tests standard AWS RDS Database Activity Streams gzip format decompression.
func TestDecompressPayload_Gzip(t *testing.T) {
	originalData := []byte("{\"databaseActivityEventList\": [{\"logTime\": \"2026-10-12 12:43:25\", \"command\": \"SELECT 1\"}]}")

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, err := gw.Write(originalData)
	if err != nil {
		t.Fatalf("failed to write gzip: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}

	decompressed, err := DecompressPayload(buf.Bytes())
	if err != nil {
		t.Fatalf("failed to decompress gzip: %v", err)
	}

	if string(decompressed) != string(originalData) {
		t.Errorf("expected %q, got %q", string(originalData), string(decompressed))
	}
}

// TestDecompressPayload_RawJSON tests that uncompressed JSON payloads pass through directly.
func TestDecompressPayload_RawJSON(t *testing.T) {
	originalJSON := []byte("{\"databaseActivityEventList\": []}")
	decompressed, err := DecompressPayload(originalJSON)
	if err != nil {
		t.Fatalf("unexpected error for raw JSON: %v", err)
	}
	if string(decompressed) != string(originalJSON) {
		t.Errorf("expected %q, got %q", string(originalJSON), string(decompressed))
	}
}

// TestDecompressPayload_UnrecognizedFormat tests that unrecognized bytes return descriptive error with magic bytes.
func TestDecompressPayload_UnrecognizedFormat(t *testing.T) {
	corrupted := []byte{0xde, 0xad, 0xbe, 0xef}
	_, err := DecompressPayload(corrupted)
	if err == nil {
		t.Fatal("expected error on unrecognized format, got nil")
	}
	if !strings.Contains(err.Error(), "unrecognized payload format") {
		t.Errorf("expected error to mention unrecognized format, got: %v", err)
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

	decompressed, err := DecompressZlib(buf.Bytes())
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
	_, err := DecompressZlib(corrupted)
	if err == nil {
		t.Fatal("expected error on corrupted zlib stream, got nil")
	}
}

// TestDecompressZlib_EmptyInput tests that empty input returns error.
func TestDecompressZlib_EmptyInput(t *testing.T) {
	_, err := DecompressZlib([]byte{})
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
	decrypted, err := DecryptAWSEncryptionSDKPayload(ctx, encOutput.Ciphertext, rawKey)
	if err != nil {
		t.Fatalf("failed to decrypt valid ciphertext: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("expected %q, got %q", string(plaintext), string(decrypted))
	}

	// 3. Decrypt with wrong key fails
	wrongKey := []byte("11111111111111111111111111111111")
	_, err = DecryptAWSEncryptionSDKPayload(ctx, encOutput.Ciphertext, wrongKey)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key, got nil")
	}

	// 4. Decrypt malformed ciphertext fails
	_, err = DecryptAWSEncryptionSDKPayload(ctx, []byte("invalid-ciphertext"), rawKey)
	if err == nil {
		t.Fatal("expected error decrypting corrupted ciphertext, got nil")
	}
}

// TestDecryptAWSEncryptionSDKPayload_NonCommittingAlgorithmSuite verifies that
// payloads encrypted using non-committing algorithm suites (the format generated by RDS DAS)
// can be successfully decrypted without triggering "Commitment policy requires only committing algorithm suites".
func TestDecryptAWSEncryptionSDKPayload_NonCommittingAlgorithmSuite(t *testing.T) {
	ctx := context.Background()
	rawKey := []byte("01234567890123456789012345678901") // 32-byte key
	plaintext := []byte("AWS RDS Database Activity Stream raw encrypted event payload")

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

	// Encrypt using a non-committing algorithm suite (as produced by RDS DAS)
	forbidEncryptPolicy := mpltypes.ESDKCommitmentPolicyForbidEncryptAllowDecrypt
	encClient, err := client.NewClient(esdktypes.AwsEncryptionSdkConfig{
		CommitmentPolicy: &forbidEncryptPolicy,
	})
	if err != nil {
		t.Fatalf("failed to create esdk client: %v", err)
	}

	nonCommittingSuite := mpltypes.ESDKAlgorithmSuiteIdAlgAes256GcmIv12Tag16HkdfSha256
	encOutput, err := encClient.Encrypt(ctx, esdktypes.EncryptInput{
		Plaintext:        plaintext,
		Keyring:          aesKeyring,
		AlgorithmSuiteId: &nonCommittingSuite,
	})
	if err != nil {
		t.Fatalf("failed to encrypt with non-committing suite: %v", err)
	}

	// Decrypt using DecryptAWSEncryptionSDKPayload (which must accept non-committing suites)
	decrypted, err := DecryptAWSEncryptionSDKPayload(ctx, encOutput.Ciphertext, rawKey)
	if err != nil {
		t.Fatalf("failed to decrypt non-committing ciphertext: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("expected %q, got %q", string(plaintext), string(decrypted))
	}
}

// TestDecryptAWSEncryptionSDKPayload_BCNamespace verifies that payloads encrypted with
// KeyNamespace "BC" (the exact provider ID embedded by AWS RDS Database Activity Streams Java JceMasterKey)
// are successfully decrypted by the MultiKeyring.
func TestDecryptAWSEncryptionSDKPayload_BCNamespace(t *testing.T) {
	ctx := context.Background()
	rawKey := []byte("01234567890123456789012345678901") // 32-byte key
	plaintext := []byte("AWS RDS DAS payload with BC provider namespace")

	matProv, err := mpl.NewClient(mpltypes.MaterialProvidersConfig{})
	if err != nil {
		t.Fatalf("failed to create mpl client: %v", err)
	}

	// Encrypt with KeyNamespace "BC" (exact RDS DAS format)
	aesKeyring, err := matProv.CreateRawAesKeyring(ctx, mpltypes.CreateRawAesKeyringInput{
		KeyName:      "DataKey",
		KeyNamespace: "BC",
		WrappingKey:  rawKey,
		WrappingAlg:  mpltypes.AesWrappingAlgAlgAes256GcmIv12Tag16,
	})
	if err != nil {
		t.Fatalf("failed to create keyring: %v", err)
	}

	forbidEncryptPolicy := mpltypes.ESDKCommitmentPolicyForbidEncryptAllowDecrypt
	encClient, err := client.NewClient(esdktypes.AwsEncryptionSdkConfig{
		CommitmentPolicy: &forbidEncryptPolicy,
	})
	if err != nil {
		t.Fatalf("failed to create esdk client: %v", err)
	}

	nonCommittingSuite := mpltypes.ESDKAlgorithmSuiteIdAlgAes256GcmIv12Tag16HkdfSha256
	encOutput, err := encClient.Encrypt(ctx, esdktypes.EncryptInput{
		Plaintext:        plaintext,
		Keyring:          aesKeyring,
		AlgorithmSuiteId: &nonCommittingSuite,
	})
	if err != nil {
		t.Fatalf("failed to encrypt with BC namespace: %v", err)
	}

	// Verify that DecryptAWSEncryptionSDKPayload unmarshals and decrypts the BC namespace
	decrypted, err := DecryptAWSEncryptionSDKPayload(ctx, encOutput.Ciphertext, rawKey)
	if err != nil {
		t.Fatalf("failed to decrypt BC namespace payload: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("expected %q, got %q", string(plaintext), string(decrypted))
	}
}

// TestDecryptAWSEncryptionSDKPayload_CustomNamespaceViaEnv verifies that a custom
// namespace configured through DAS_KEY_NAMESPACE is properly recognized and decrypted.
func TestDecryptAWSEncryptionSDKPayload_CustomNamespaceViaEnv(t *testing.T) {
	ctx := context.Background()
	rawKey := []byte("01234567890123456789012345678901") // 32-byte key
	plaintext := []byte("Custom namespace payload test")

	customNS := "CustomCorporateMasterKeyProvider"
	t.Setenv("DAS_KEY_NAMESPACE", customNS)

	matProv, err := mpl.NewClient(mpltypes.MaterialProvidersConfig{})
	if err != nil {
		t.Fatalf("failed to create mpl client: %v", err)
	}

	aesKeyring, err := matProv.CreateRawAesKeyring(ctx, mpltypes.CreateRawAesKeyringInput{
		KeyName:      "DataKey",
		KeyNamespace: customNS,
		WrappingKey:  rawKey,
		WrappingAlg:  mpltypes.AesWrappingAlgAlgAes256GcmIv12Tag16,
	})
	if err != nil {
		t.Fatalf("failed to create keyring: %v", err)
	}

	forbidEncryptPolicy := mpltypes.ESDKCommitmentPolicyForbidEncryptAllowDecrypt
	encClient, err := client.NewClient(esdktypes.AwsEncryptionSdkConfig{
		CommitmentPolicy: &forbidEncryptPolicy,
	})
	if err != nil {
		t.Fatalf("failed to create esdk client: %v", err)
	}

	nonCommittingSuite := mpltypes.ESDKAlgorithmSuiteIdAlgAes256GcmIv12Tag16HkdfSha256
	encOutput, err := encClient.Encrypt(ctx, esdktypes.EncryptInput{
		Plaintext:        plaintext,
		Keyring:          aesKeyring,
		AlgorithmSuiteId: &nonCommittingSuite,
	})
	if err != nil {
		t.Fatalf("failed to encrypt with custom namespace: %v", err)
	}

	decrypted, err := DecryptAWSEncryptionSDKPayload(ctx, encOutput.Ciphertext, rawKey)
	if err != nil {
		t.Fatalf("failed to decrypt custom namespace payload: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("expected %q, got %q", string(plaintext), string(decrypted))
	}
}
