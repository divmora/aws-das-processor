.PHONY: build clean test test-coverage dev-setup fmt lint cfn-lint lambda-package docker-build docker-build-multiarch

build:
	@go build -o bin/aws-das-processor ./...

test:
	@go test -v ./...

fmt:
	@go fmt ./...

lint:
	@golangci-lint run

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
