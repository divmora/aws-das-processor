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
