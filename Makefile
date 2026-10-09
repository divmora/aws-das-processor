.PHONY: build clean test test-coverage dev-setup fmt lint cfn-lint lambda-package docker-build docker-build-multiarch docs-serve

PORT ?= 8080

build:
	@go build -o bin/aws-das-processor ./...

test:
	@go test -v ./...

fmt:
	@go fmt ./...

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	elif [ -x "$$(go env GOPATH)/bin/golangci-lint" ]; then \
		"$$(go env GOPATH)/bin/golangci-lint" run; \
	else \
		echo "golangci-lint not found (run 'make dev-setup')"; \
		exit 1; \
	fi

cfn-lint: ## Lint CloudFormation templates (requires cfn-lint)
	@echo "==> Linting CloudFormation templates"
	@if command -v cfn-lint >/dev/null 2>&1; then \
		cfn-lint deploy/cloudformation/*.yaml; \
	elif [ -x "$$HOME/Library/Python/3.12/bin/cfn-lint" ]; then \
		"$$HOME/Library/Python/3.12/bin/cfn-lint" deploy/cloudformation/*.yaml; \
	elif [ -x "$$HOME/.local/bin/cfn-lint" ]; then \
		"$$HOME/.local/bin/cfn-lint" deploy/cloudformation/*.yaml; \
	else \
		echo "cfn-lint not found in PATH (install via 'pip install cfn-lint')"; \
	fi

clean:
	@rm -rf bin/

docker-build:
	@docker build -t aws-das-processor:latest .

docker-build-multiarch:
	@docker buildx build --platform linux/amd64,linux/arm64 -t aws-das-processor:latest .

test-coverage:
	@mkdir -p bin
	@go test -coverprofile=bin/coverage.out ./...
	@go tool cover -html=bin/coverage.out -o bin/coverage.html || true
	@echo "Coverage report: bin/coverage.html"

dev-setup:
	@go mod download
	@go install honnef.co/go/tools/cmd/staticcheck@latest || true
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest || true
	@echo "Development environment ready."

lambda-package:
	@mkdir -p bin
	@GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/bootstrap main.go filter.go
	@cd bin && zip -j aws-das-processor.zip bootstrap
	@echo "Lambda package: bin/aws-das-processor.zip"

docs-serve:
	@echo "Serving documentation at http://localhost:$(PORT)..."
	@python3 -m http.server $(PORT) --directory docs
 