# AWS DAS Processor

[![Latest Release](https://img.shields.io/github/v/release/divmora/aws-das-processor?logo=github)](https://github.com/divmora/aws-das-processor/releases)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![CI/CD](https://github.com/divmora/aws-das-processor/actions/workflows/ci.yml/badge.svg)](https://github.com/divmora/aws-das-processor/actions)
[![Go Version](https://img.shields.io/github/go-mod/go-version/divmora/aws-das-processor)](go.mod)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/divmora/aws-das-processor)
[![Security Policy](https://img.shields.io/badge/Security-Policy-green.svg)](SECURITY.md)

## Architecture

<p align="center">
  <img src="docs/assets/architecture.png" alt="aws-das-processor Architecture Diagram" width="100%">
</p>

## Overview & Key Features
AWS DAS Processor is a Lambda function that consumes AWS Database Activity Stream events via an SQS fanout from S3. It decrypts, decompresses, filters, and writes the stream data back to S3.

## Configuration / Environment Variables
| Variable | Default | Description |
|---|---|---|
| `DAS_FILTER_NAME` | | The specific filter stream name |
| `DAS_KMS_REGION_NAME` | | Region where the KMS key resides |
| `DAS_RDS_RESOURCE_ID` | | ID of the RDS resource for encryption context |

## IAM Permissions
Requires `s3:GetObject`, `s3:PutObject`, `kms:Decrypt`, and `sqs` operations.

## Development & Building
- Build: `make build`
- Test: `make test`

## Community Links
- [Contributing](CONTRIBUTING.md)
- [Security](SECURITY.md)

## License & Commercial Use
Licensed under the Apache License 2.0.
