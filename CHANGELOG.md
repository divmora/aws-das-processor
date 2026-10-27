# Changelog

## [0.2.0](https://github.com/divmora/aws-das-processor/compare/v0.1.0...v0.2.0) (2026-10-27)


### Features

* add cloudformation template for sns fan-out deployment ([1e88f2c](https://github.com/divmora/aws-das-processor/commit/1e88f2ca56dbe6b9d7ebe0f69af9d0f5a70ad786))
* add configurable batch size, batching window, and concurrency scaling to CloudFormation ([8cd1714](https://github.com/divmora/aws-das-processor/commit/8cd17146fb6332c52c765b6f7c131b972ced4def))
* add DasFiltersDir and kms:GenerateDataKey to CloudFormation templates ([f9fc20e](https://github.com/divmora/aws-das-processor/commit/f9fc20e911a5301a1d8e8c58f0bac53ca5da853a))
* complete S3 DAS log processing pipeline ([1857c28](https://github.com/divmora/aws-das-processor/commit/1857c289cb7682bd317ef6dc36dd589d8dc7d9ea))
* default KmsKeyArn to wildcard for multi-account support in CloudFormation ([1d02515](https://github.com/divmora/aws-das-processor/commit/1d02515c07019c5b728d48bb2849214d390832ef))
* **deploy:** add DLQ, log retention, and existing destination bucket to CloudFormation ([#18](https://github.com/divmora/aws-das-processor/issues/18)) ([#25](https://github.com/divmora/aws-das-processor/issues/25)) ([b67e0b7](https://github.com/divmora/aws-das-processor/commit/b67e0b70f8f5639bb6d011df5088bbe172d67261))
* **deploy:** align CloudFormation template path to deploy/cloudformation/lambda.yaml and add cfn-ci ([73d439e](https://github.com/divmora/aws-das-processor/commit/73d439e944d49eba3897e496ad13532ad061cfce))
* handle s3:TestEvent gracefully without false error alarms (closes [#19](https://github.com/divmora/aws-das-processor/issues/19)) ([70cf6d8](https://github.com/divmora/aws-das-processor/commit/70cf6d8c22e6491cff14f35a1ae442d07b2c0659))
* implement multi-filter matching engine with single-decrypt fanout and self-routing ([40628c6](https://github.com/divmora/aws-das-processor/commit/40628c638249c17f50f0ccc6e15587c556b9d27b))
* provide Processor.Handler method for clean dependency injection (closes [#4](https://github.com/divmora/aws-das-processor/issues/4)) ([0cc0972](https://github.com/divmora/aws-das-processor/commit/0cc097200075842ff0d53ba1f3cbe586bb50bfcd))
* support configurable filter path with LAMBDA_TASK_ROOT and in-memory caching ([#20](https://github.com/divmora/aws-das-processor/issues/20)) ([e3c8124](https://github.com/divmora/aws-das-processor/commit/e3c8124e513a2286c34777e4456cb16a9f20b9d8))
* support Lambda function reserved concurrency in CloudFormation templates ([25460e5](https://github.com/divmora/aws-das-processor/commit/25460e51cc56d62988d354f002dd378f53bff6d0))
* support modern EventBridge and classic SNS ingestion with error prefix filtering ([3bcef2f](https://github.com/divmora/aws-das-processor/commit/3bcef2fdde9bef110e870eefc24624bbab0abd86))
* support multiple input s3 buckets and exclude error manifests in EventBridge ([95fe1f5](https://github.com/divmora/aws-das-processor/commit/95fe1f59a519c5ef1306e17b12c09b891fa0d349))
* support per-filter destination S3 bucket and KMS encryption key ([f2373ca](https://github.com/divmora/aws-das-processor/commit/f2373ca7881c1e914a12546e51835776a7416999))


### Bug Fixes

* add missing Dockerfiles for CI/CD pipeline ([e50edc7](https://github.com/divmora/aws-das-processor/commit/e50edc75a32425748d2ad2c8ab1a77fb7997369d))
* allow decrypting non-committing algorithm suites for RDS DAS payloads ([f2552fd](https://github.com/divmora/aws-das-processor/commit/f2552fdc65efacf068ed73b958a8a5109a65deb7))
* **cfn:** correct AWSLambdaBasicExecutionRole managed policy ARN format ([9120ab3](https://github.com/divmora/aws-das-processor/commit/9120ab3aeae31ba2f1d3061cff59039fb58a97cf))
* implement SQS partial batch failure handling ([#3](https://github.com/divmora/aws-das-processor/issues/3)) ([#23](https://github.com/divmora/aws-das-processor/issues/23)) ([029989b](https://github.com/divmora/aws-das-processor/commit/029989b53711e678c17db2f2ff4068d2de3e967b))
* inspect magic header bytes to decompress GZIP, ZLIB, and raw JSON payloads ([fc77ed3](https://github.com/divmora/aws-das-processor/commit/fc77ed3dd59f6285808fc7641844d74eaa3ea1ee))
* prevent S3 response body resource leak using deferred close (closes [#7](https://github.com/divmora/aws-das-processor/issues/7)) ([42cdb98](https://github.com/divmora/aws-das-processor/commit/42cdb987ba92dc815033071cb1ca0908fcba0d3f))
* prevent silent error swallowing and add observability to Parquet converter (closes [#9](https://github.com/divmora/aws-das-processor/issues/9)) ([79b9282](https://github.com/divmora/aws-das-processor/commit/79b9282e0cf5e32c45ab9ab53fd73a3a13b3fb3b))
* sanitize nil string representation in parquet conversion ([3a59b3f](https://github.com/divmora/aws-das-processor/commit/3a59b3f323e73c2e69fde562a82607dd04fa7c1a))
* support BC and custom key provider namespaces via MultiKeyring for RDS DAS decryption ([6c2e98e](https://github.com/divmora/aws-das-processor/commit/6c2e98e157dd922386d0d8bec078c136b8af171d))
* support multi-record concatenated Firehose payloads in S3 chunks ([cdd8e25](https://github.com/divmora/aws-das-processor/commit/cdd8e25b3f237134bb40cacc2d3b1b6e91272852))
* switch SQS queues to SqsManagedSseEnabled to allow service principal delivery ([7c0984a](https://github.com/divmora/aws-das-processor/commit/7c0984a3d8d8c4ba726cc56b08a4595a1fb605c5))
* URL-decode S3 object keys from event notifications before GetObject (closes [#14](https://github.com/divmora/aws-das-processor/issues/14)) ([0b98a61](https://github.com/divmora/aws-das-processor/commit/0b98a61d6685c811f06cf6ab60e1ec8183f91d6d))
* use golang:1.26-alpine to match go.mod requirements ([9366951](https://github.com/divmora/aws-das-processor/commit/936695184d49c0dfc539ef7d26a8cca9e72f025a))
* validate RDS resource ID before KMS decrypt and log diagnostics (closes [#5](https://github.com/divmora/aws-das-processor/issues/5)) ([102a8a6](https://github.com/divmora/aws-das-processor/commit/102a8a67ee918f716d309e093765a6666c553d98))


### Performance Improvements

* eliminate redundant JSON marshal/unmarshal in convertJSONToParquet ([#16](https://github.com/divmora/aws-das-processor/issues/16)) ([#26](https://github.com/divmora/aws-das-processor/issues/26)) ([7e5a45d](https://github.com/divmora/aws-das-processor/commit/7e5a45db71e38916147aff07ae52f8297ff623a5))
