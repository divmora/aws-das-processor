# Changelog

## [0.2.0](https://github.com/divmora/aws-das-processor/compare/v0.1.0...v0.2.0) (2026-09-29)


### Features

* add cloudformation template for sns fan-out deployment ([1e88f2c](https://github.com/divmora/aws-das-processor/commit/1e88f2ca56dbe6b9d7ebe0f69af9d0f5a70ad786))
* complete S3 DAS log processing pipeline ([1857c28](https://github.com/divmora/aws-das-processor/commit/1857c289cb7682bd317ef6dc36dd589d8dc7d9ea))
* **deploy:** add DLQ, log retention, and existing destination bucket to CloudFormation ([#18](https://github.com/divmora/aws-das-processor/issues/18)) ([#25](https://github.com/divmora/aws-das-processor/issues/25)) ([b67e0b7](https://github.com/divmora/aws-das-processor/commit/b67e0b70f8f5639bb6d011df5088bbe172d67261))
* **deploy:** align CloudFormation template path to deploy/cloudformation/lambda.yaml and add cfn-ci ([73d439e](https://github.com/divmora/aws-das-processor/commit/73d439e944d49eba3897e496ad13532ad061cfce))


### Bug Fixes

* add missing Dockerfiles for CI/CD pipeline ([e50edc7](https://github.com/divmora/aws-das-processor/commit/e50edc75a32425748d2ad2c8ab1a77fb7997369d))
* implement SQS partial batch failure handling ([#3](https://github.com/divmora/aws-das-processor/issues/3)) ([#23](https://github.com/divmora/aws-das-processor/issues/23)) ([029989b](https://github.com/divmora/aws-das-processor/commit/029989b53711e678c17db2f2ff4068d2de3e967b))
* use golang:1.26-alpine to match go.mod requirements ([9366951](https://github.com/divmora/aws-das-processor/commit/936695184d49c0dfc539ef7d26a8cca9e72f025a))


### Performance Improvements

* eliminate redundant JSON marshal/unmarshal in convertJSONToParquet ([#16](https://github.com/divmora/aws-das-processor/issues/16)) ([#26](https://github.com/divmora/aws-das-processor/issues/26)) ([7e5a45d](https://github.com/divmora/aws-das-processor/commit/7e5a45db71e38916147aff07ae52f8297ff623a5))
