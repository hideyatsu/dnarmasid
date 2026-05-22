package storage

import (
	"context"
	"io"
)

// StorageService defines the interface for file storage operations.
type StorageService interface {
	// UploadFile uploads a byte slice to the storage service and returns the public URL.
	UploadFile(ctx context.Context, fileName string, content []byte, contentType string) (publicURL string, err error)

	// UploadReader uploads a stream to the storage service and returns the public URL.
	UploadReader(ctx context.Context, fileName string, reader io.Reader, contentType string) (publicURL string, err error)

	// DeleteFile removes a file from the storage service.
	DeleteFile(ctx context.Context, fileName string) error
}
