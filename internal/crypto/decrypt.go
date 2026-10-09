package crypto

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"io"

	mpl "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygenerated"
	mpltypes "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygeneratedtypes"
	client "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygenerated"
	esdktypes "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygeneratedtypes"
)

// MaxDecompressedSize limits decompression to 256MB to mitigate decompression bombs.
const MaxDecompressedSize = 256 * 1024 * 1024

// DecryptAWSEncryptionSDKPayload uses the plaintext data key to decrypt the AWS Encryption SDK message format.
func DecryptAWSEncryptionSDKPayload(ctx context.Context, ciphertext, plaintextDataKey []byte) ([]byte, error) {
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

// DecompressZlib decompresses zlib formatted data with decompression bomb protection.
func DecompressZlib(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	var out bytes.Buffer
	if _, err := io.Copy(&out, io.LimitReader(reader, MaxDecompressedSize)); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
