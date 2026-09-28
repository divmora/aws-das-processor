.PHONY: build clean test test-coverage dev-setup fmt lint lambda-package docker-build docker-build-multiarch

build:
	@go build -o bin/aws-das-processor ./...

test:
	@go test -v ./...

fmt:
	@go fmt ./...

lint:
	@golangci-lint run

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