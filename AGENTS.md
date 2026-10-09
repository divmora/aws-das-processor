# Workspace Guidelines & Agent Instructions

Welcome to the **AWS Database Activity Streams (DAS) Processor** repository. These instructions apply to all AI coding agents (Antigravity, Cursor, GitHub Copilot, Claude Code, etc.) and human contributors working within `divmora/aws-das-processor`.

---

## 1. Project Overview & Architecture

`aws-das-processor` is a high-performance Go AWS Lambda consumer designed to process AWS RDS Database Activity Streams (DAS).

### Event Ingestion & Processing Flow
1. **S3 Event Notification**: RDS writes encrypted, zlib-compressed DAS records to a source S3 bucket.
2. **Fanout via SNS & SQS**: An S3 bucket notification publishes an event to an SNS topic, which fans out to an SQS queue.
3. **Lambda Trigger**: AWS Lambda polls the SQS queue using partial batch failure handling (`ReportBatchItemFailures`).
4. **Envelope Decryption**: Lambda downloads the raw log chunk from S3 and decrypts the encrypted payload using AWS KMS and the AWS Encryption SDK for Go (`github.com/aws/aws-encryption-sdk/releases/go/encryption-sdk`) with the RDS Resource ID encryption context.
5. **Decompression & Parsing**: The decrypted stream is decompressed via `compress/zlib` and parsed into structured JSON database activity records.
6. **Filtering Engine**: Events are evaluated against configurable filter rules (loaded from YAML configs in `filters/` or environment definitions) to drop noise (e.g. heartbeat queries, drop fields, user/dimension selectors).
7. **Parquet Serialization & S3 Sink**: Retained records are serialized into compressed Apache Parquet format using `parquet-go` and written back to an analytical S3 destination bucket partitioned by date and cluster identifier.

---

## 2. Licensing Policy (BSL 1.1)

This repository is licensed under the **Business Source License 1.1 (BSL 1.1)**, adhering to the DIVMORA organization Tier 2 licensing standard:
- **Licensor**: DIVMORA Technologies
- **Licensed Work**: AWS Database Activity Streams (DAS) Processor, including all source code, documentation, and associated files in this repository.
- **Additional Use Grant**: Free for non-production use, including local development, testing, staging, QA, CI/CD automated validation, educational purposes, and proof-of-concept evaluation.
- **Production Requirement**: Deploying or executing in a production environment, or offering as a commercial product or hosted/managed service, requires a valid commercial license (EULA) from DIVMORA Technologies (`licensing@divmora.com`).
- **Change Date**: Converts to the Apache License, Version 2.0 three (3) years from the date of release of the specific version.
- **Reference**: See [LICENSE](LICENSE) and the organization [LICENSING.md](https://github.com/divmora/.github/blob/main/LICENSING.md).

---

## 3. Core Engineering Guidelines

### 3.1 Go Engineering Standards
1. **Structured Logging (`log/slog`)**: Use Go standard library `log/slog` with JSON output. Do not use unstructured `fmt.Println` or `log.Printf`.
2. **Safety & Dry-Run Guarantee**: Any destructive, mutating, or write operations against AWS or external cloud resources **MUST** support a dry-run mode (e.g., `approve bool` or dry-run flag) to prevent unintended mutations.
3. **AWS SDK v2 Patterns**:
   - Always propagate `context.Context`.
   - Initialize clients explicitly.
   - Handle API pagination properly to prevent silent truncation.
   - Wrap errors with contextual information using `fmt.Errorf("action failed: %w", err)`.
4. **Memory & Stream Efficiency**: Decompress, filter, and stream JSON records to Parquet with minimal buffer allocations to optimize Lambda runtime memory.
5. **SQS Partial Batch Failure**: Always return individual failing message IDs in `ReportBatchItemFailures` so that successful messages in the batch are committed and only failed messages are retried.

### 3.2 Docker & Containerization Standards
1. **Multi-Stage Builds**: Multi-stage `Dockerfile` and `Dockerfile.lambda` using minimal base images (`golang:1.26-alpine` builder and `alpine:latest` or `public.ecr.aws/lambda/provided:al2023` runtime).
2. **Build Context Optimization (`.dockerignore`)**: A root `.dockerignore` file **MUST** be maintained to exclude `.git/`, `.github/`, documentation, test suites, local binaries (`bin/`), and editor artifacts from the build context (per org guidelines §4.8.6).

### 3.3 Conventional Commits & Versioning
All commits and Pull Request titles **MUST** strictly follow the [Conventional Commits](https://www.conventionalcommits.org/) specification:
- `feat:` for new features (bumps minor version in Release Please).
- `fix:` for bug fixes (bumps patch version).
- `docs:` for documentation updates.
- `refactor:` for refactoring without behavior changes.
- `test:` for test additions or updates.
- `chore:` for dependencies, tooling, or internal configuration.
- `feat!:` or `fix!:` for breaking changes (bumps major version).

### 3.4 Living Product Roadmap Management
`ROADMAP.md` is the central living document tracking future capabilities, optimizations, and technical debt:
- **Adding Items**: When identifying a capability, optimization, or architectural improvement for future work, add it to `ROADMAP.md` under the appropriate category.
- **Deduplication with GitHub Issues**: If an active GitHub Issue already exists or is explicitly created for a task, **do not duplicate it in `ROADMAP.md`**. GitHub Issues track active, assigned, or triaged tasks, while `ROADMAP.md` captures high-level, unassigned architectural vision and backlog capabilities.
- **Removing Items**: Once a feature is fully implemented, verified with tests, and committed, **remove it from `ROADMAP.md`** immediately to keep the roadmap focused on active upcoming tasks.

---

## 4. Verification Commands Matrix

Always verify changes locally before finalizing tasks:

| Command | Action |
| :--- | :--- |
| `make fmt` | Format Go code (`go fmt ./...`) |
| `make lint` | Run linter (`golangci-lint run`) |
| `make test` | Run all unit tests (`go test -v ./...`) |
| `make test-coverage` | Run tests with coverage profile |
| `make build` | Build binary locally (`go build -o bin/aws-das-processor ./...`) |
| `make cfn-lint` | Lint CloudFormation templates (`cfn-lint deploy/cloudformation/*.yaml`) |
| `make lambda-package` | Build Linux/amd64 bootstrap binary and zip Lambda package |
| `make docker-build` | Build container image locally |
| `make docs-serve` | Serve documentation locally on port 8080 |
| `make clean` | Clean up build artifacts |
