package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/divmora/aws-das-processor/internal/processor"
	"github.com/divmora/aws-das-processor/pkg/filter"
)

var proc *processor.Processor

func init() {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("Unable to load AWS config", "error", err)
		os.Exit(1)
	}
	s3Client := s3.NewFromConfig(cfg)
	kmsClient := kms.NewFromConfig(cfg)
	proc = processor.New(s3Client, kmsClient)

	// Pre-load all filter configurations using resolved filters directory
	filtersDir := filter.ResolveFiltersDir()
	configs, err := filter.LoadAllFilterConfigs(filtersDir)
	if err != nil {
		slog.Warn("Failed to pre-load filter configs from filters directory", "dir", filtersDir, "error", err)
	} else if len(configs) > 0 {
		proc.SetFilterConfigs(configs)
		slog.Info("Successfully pre-loaded filter configurations", "dir", filtersDir, "count", len(configs))
	} else {
		slog.Warn("No filter configurations loaded from filters directory", "dir", filtersDir)
	}

	if envResID := strings.TrimSpace(os.Getenv("DAS_RDS_RESOURCE_ID")); envResID != "" {
		slog.Info("Configured static fallback RDS Resource ID", "ResourceID", envResID)
	} else {
		slog.Info("No static DAS_RDS_RESOURCE_ID configured; RDS Resource ID will be dynamically discovered from S3 object key paths")
	}
}

func handler(ctx context.Context, sqsEvent events.SQSEvent) (events.SQSEventResponse, error) {
	return proc.Handler(ctx, sqsEvent)
}

func main() {
	lambda.Start(handler)
}
