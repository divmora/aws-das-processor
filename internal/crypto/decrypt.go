package crypto

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"log/slog"
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

// DecompressPayload inspects the payload's magic header bytes prior to decompression:
// 1. GZIP (magic bytes 0x1F, 0x8B per RFC 1952) - standard AWS RDS Database Activity Streams format.
// 2. ZLIB (RFC 1950: CMF=0x78 or (CMF&0x0F)==8, and (CMF*256+FLG)%31 == 0).
// 3. Raw JSON (starts with '{' or '[').
// It decompresses with decompression bomb protection (capped at MaxDecompressedSize).
func DecompressPayload(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty input payload")
	}

	// 1. Check for uncompressed JSON (starts with '{' or '[')
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		slog.Debug("Detected uncompressed JSON payload, skipping decompression")
		return data, nil
	}

	// 2. Inspect for GZIP header (0x1F, 0x8B) - standard AWS RDS Database Activity Streams format
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		slog.Debug("Detected GZIP compressed payload, decompressing via compress/gzip")
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("failed to initialize gzip reader: %w", err)
		}
		defer reader.Close()

		var out bytes.Buffer
		if _, err := io.Copy(&out, io.LimitReader(reader, MaxDecompressedSize)); err != nil {
			return nil, fmt.Errorf("failed to decompress gzip payload: %w", err)
		}
		return out.Bytes(), nil
	}

	// 3. Inspect for ZLIB header (RFC 1950: CMF & 0x0F == 8 and (CMF*256 + FLG) % 31 == 0)
	if len(data) >= 2 && (data[0]&0x0F) == 8 && (int(data[0])*256+int(data[1]))%31 == 0 {
		slog.Debug("Detected ZLIB compressed payload, decompressing via compress/zlib")
		reader, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("failed to initialize zlib reader: %w", err)
		}
		defer reader.Close()

		var out bytes.Buffer
		if _, err := io.Copy(&out, io.LimitReader(reader, MaxDecompressedSize)); err != nil {
			return nil, fmt.Errorf("failed to decompress zlib payload: %w", err)
		}
		return out.Bytes(), nil
	}

	headerPreview := ""
	for i := 0; i < len(data) && i < 4; i++ {
		headerPreview += fmt.Sprintf("%02x ", data[i])
	}
	return nil, fmt.Errorf("unrecognized payload format (magic bytes: %s; expected gzip 1f 8b, zlib 78 xx, or JSON)", strings.TrimSpace(headerPreview))
}

// DecompressZlib is an alias for DecompressPayload preserving backward compatibility.
func DecompressZlib(data []byte) ([]byte, error) {
	return DecompressPayload(data)
}
