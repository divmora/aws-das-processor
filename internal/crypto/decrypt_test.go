package crypto

import (
	"bytes"
	"compress/zlib"
	"context"
	"testing"

	mpl "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygenerated"
	mpltypes "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygeneratedtypes"
	client "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygenerated"
	esdktypes "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygeneratedtypes"
)

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
