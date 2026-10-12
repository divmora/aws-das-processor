package crypto

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	mpl "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygenerated"
	mpltypes "github.com/aws/aws-cryptographic-material-providers-library/releases/go/mpl/awscryptographymaterialproviderssmithygeneratedtypes"
	client "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygenerated"
	esdktypes "github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk/awscryptographyencryptionsdksmithygeneratedtypes"
)

// MaxDecompressedSize limits decompression to 256MB to mitigate decompression bombs.
const MaxDecompressedSize = 256 * 1024 * 1024

// DecryptAWSEncryptionSDKPayload uses the plaintext data key to decrypt the AWS Encryption SDK message format.
// It instantiates a MultiKeyring supporting "BC" (standard RDS DAS BouncyCastle provider),
// "RawMasterKeyProvider", and any custom namespace configured via DAS_KEY_NAMESPACE.
func DecryptAWSEncryptionSDKPayload(ctx context.Context, ciphertext, plaintextDataKey []byte) ([]byte, error) {
	matProv, err := mpl.NewClient(mpltypes.MaterialProvidersConfig{})
	if err != nil {
		return nil, fmt.Errorf("failed to create material providers client: %w", err)
	}

	keyName := os.Getenv("DAS_KEY_NAME")
	if keyName == "" {
		keyName = "DataKey"
	}

	// Supported key namespaces:
	// 1. "BC" - BouncyCastle JceMasterKey provider (standard AWS RDS Database Activity Streams)
	// 2. "RawMasterKeyProvider" - Python AWS Encryption SDK raw AES master key provider
	// 3. Custom namespace via DAS_KEY_NAMESPACE if configured
	candidateNamespaces := []string{"BC", "RawMasterKeyProvider"}
	if customNS := strings.TrimSpace(os.Getenv("DAS_KEY_NAMESPACE")); customNS != "" {
		candidateNamespaces = append([]string{customNS}, candidateNamespaces...)
	}

	var childKeyrings []mpltypes.IKeyring
	for _, ns := range candidateNamespaces {
		kr, err := matProv.CreateRawAesKeyring(ctx, mpltypes.CreateRawAesKeyringInput{
			KeyName:      keyName,
			KeyNamespace: ns,
			WrappingKey:  plaintextDataKey,
			WrappingAlg:  mpltypes.AesWrappingAlgAlgAes256GcmIv12Tag16,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create raw aes keyring for namespace %q: %w", ns, err)
		}
		childKeyrings = append(childKeyrings, kr)
	}

	keyring, err := matProv.CreateMultiKeyring(ctx, mpltypes.CreateMultiKeyringInput{
		ChildKeyrings: childKeyrings,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create multi keyring: %w", err)
	}

	commitmentPolicy := mpltypes.ESDKCommitmentPolicyRequireEncryptAllowDecrypt
	encryptionClient, err := client.NewClient(esdktypes.AwsEncryptionSdkConfig{
		CommitmentPolicy: &commitmentPolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create encryption sdk client: %w", err)
	}

	decryptOutput, err := encryptionClient.Decrypt(ctx, esdktypes.DecryptInput{
		Ciphertext: ciphertext,
		Keyring:    keyring,
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
