# Living Product Roadmap

This document captures high-level architectural vision, upcoming capabilities, and technical backlog for `aws-das-processor`. Completed items are pruned upon release to maintain focus on upcoming work. Active tasks with dedicated GitHub Issues are tracked in the issue tracker rather than duplicated here.

---

## 🛡️ SQL Anomaly Detection & Threat Intelligence
- [ ] **SQL Query Fingerprinting**: Tokenize and fingerprint incoming queries to build per-user and per-application normal execution baselines.
- [ ] **Volumetric & Rate Spike Detection**: Detect anomalous query burst frequencies indicative of bulk data exfiltration or automated enumeration.
- [ ] **Administrative & DDL/DCL Alerting**: Real-time detection and tagging of unauthorized schema modifications (`DROP`, `ALTER`), permission grants (`GRANT`, `REVOKE`), and user provisioning.
- [ ] **Error-Based Exploitation Detection**: Flag abnormal clusters of database syntax and authorization errors matching SQL injection patterns.

---

## 🔒 Data Protection & PII Masking
- [ ] **Sensitive Field Masking & Tokenization**: Automated masking and cryptographic hashing of sensitive query literals (e.g., credit card numbers, SSNs, API tokens, passwords) before Parquet persistence.
- [ ] **Selective Column Encryption**: Support field-level KMS encryption for high-sensitivity audit columns.

---

## 🗄️ Multi-Engine Normalization & Adapters
- [ ] **Aurora PostgreSQL / MySQL Normalization**: Standardized unified record schema bridging PostgreSQL and MySQL DAS payload differences.
- [ ] **Enterprise Engine Adapters**: Dedicated parsing and event mapping for RDS Oracle and RDS SQL Server DAS records.

---

## 📡 SIEM & OpenTelemetry Streaming
- [ ] **Direct OTLP Telemetry Exporter**: Support streaming filtered audit events directly to OpenTelemetry collectors (compatible with Datadog, Splunk, Dynatrace, New Relic) alongside S3 storage.
- [ ] **CloudWatch EMF Compliance Metrics**: Publish metrics on event throughput, dropped queries, anomaly detection counts, and processing latency.

---

## 🔑 Enterprise Licensing & Governance
- [ ] **`license-go` Commercial Verification**: Preflight validation of DIVMORA commercial Ed25519 license tokens in production environments.
- [ ] **Offline & Online CRL Support**: Certificate Revocation List synchronization for air-gapped and connected serverless environments.
- [ ] **Fair-Use Quota Tracking**: Non-blocking resource and throughput metering for enterprise commercial tiers.
