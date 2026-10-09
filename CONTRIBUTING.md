# Contributing Guidelines

Thank you for your interest in contributing to the **AWS Database Activity Streams (DAS) Processor**!

---

## 1. Prerequisites
- **Go**: 1.26+
- **Make**: Standard GNU Make
- **Docker**: For local container image builds
- **golangci-lint**: For local static analysis (`make lint`)

---

## 2. Development Workflow

### Useful Make Targets
- `make fmt`: Format Go source files (`go fmt ./...`)
- `make lint`: Run `golangci-lint` to check code quality
- `make test`: Run all unit tests
- `make test-coverage`: Run unit tests and generate HTML coverage reports in `bin/`
- `make build`: Build the processor binary locally into `bin/`
- `make cfn-lint`: Lint CloudFormation deployment templates
- `make lambda-package`: Build and package the production Lambda zip bundle
- `make docker-build`: Build the container image locally
- `make clean`: Clean up compiled binaries and build artifacts

---

## 3. Pull Request Guidelines

### Conventional Commits
All commit messages and Pull Request titles **MUST** follow the [Conventional Commits](https://www.conventionalcommits.org/) specification:
- `feat:` new feature or functionality
- `fix:` bug fix
- `docs:` documentation updates
- `test:` adding or updating tests
- `refactor:` code changes without feature or bug modifications
- `chore:` dependency updates, tooling, or repository maintenance

### Quality Checks
Before submitting a PR, ensure all checks pass locally:
```bash
make fmt
make lint
make test
```

---

## 4. Contributor License Agreement

By contributing to this repository, you agree that your contributions will be licensed under the project's **Business Source License 1.1 (BSL 1.1)**, including its Additional Use Grant for non-production use and eventual conversion to the Apache License, Version 2.0 on the Change Date.
