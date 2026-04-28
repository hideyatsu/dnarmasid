package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"dnarmasid/shared/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssdkconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type R2Uploader struct {
	client   *s3.Client
	uploader *manager.Uploader
	bucket   string
	domain   string
}

func NewR2Uploader(cfg *config.Config) (*R2Uploader, error) {
	if cfg.R2AccessKey == "" || cfg.R2SecretKey == "" || cfg.R2BucketName == "" {
		return nil, fmt.Errorf("missing R2 configuration")
	}

	// Cloudflare R2 endpoint is based on the Account ID
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.R2AccountID)

	// Using aws.Config from the v2 config package
	awsCfg, err := awssdkconfig.LoadDefaultConfig(context.TODO(),
		awssdkconfig.WithRegion("auto"),
		awssdkconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.R2AccessKey, cfg.R2SecretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	uploader := manager.NewUploader(client)

	return &R2Uploader{
		client:   client,
		uploader: uploader,
		bucket:   cfg.R2BucketName,
		domain:   cfg.R2PublicDomain,
	}, nil
}

func (r *R2Uploader) UploadFile(ctx context.Context, fileName string, content []byte, contentType string) (string, error) {
	return r.UploadReader(ctx, fileName, bytes.NewReader(content), contentType)
}

func (r *R2Uploader) UploadReader(ctx context.Context, fileName string, reader io.Reader, contentType string) (string, error) {
	_, err := r.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(r.bucket),
		Key:         aws.String(fileName),
		Body:        reader,
		ContentType: aws.String(contentType),
	})

	if err != nil {
		return "", fmt.Errorf("R2 upload failed: %w", err)
	}

	// Construct public URL
	publicURL := fmt.Sprintf("https://%s/%s", r.domain, fileName)
	return publicURL, nil
}

func (r *R2Uploader) DeleteFile(ctx context.Context, fileName string) error {
	_, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(fileName),
	})
	if err != nil {
		return fmt.Errorf("R2 delete failed: %w", err)
	}
	return nil
}
