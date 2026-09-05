package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/Linjiahao666/server/internal/config"
)

// CompletedPart identifies a finished multipart upload part.
type CompletedPart struct {
	PartNumber int
	ETag       string
}

// PresignedPart is a presigned URL for uploading one multipart part.
type PresignedPart struct {
	PartNumber int    `json:"part_number"`
	UploadURL  string `json:"upload_url"`
}

// ObjectInfo describes stored object metadata.
type ObjectInfo struct {
	SizeBytes   int64
	ContentType string
}

// ObjectStore abstracts MinIO object operations.
type ObjectStore interface {
	PutObject(ctx context.Context, objectKey, contentType string, reader io.Reader, size int64) error
	RemoveObject(ctx context.Context, objectKey string) error
	StatObject(ctx context.Context, objectKey string) (ObjectInfo, error)
	GetObject(ctx context.Context, objectKey string, offset, length int64) (io.ReadCloser, error)
	CreateMultipartUpload(ctx context.Context, objectKey, contentType string) (string, error)
	PresignUploadParts(ctx context.Context, objectKey, uploadID string, partCount int, expiry time.Duration) ([]PresignedPart, error)
	CompleteMultipartUpload(ctx context.Context, objectKey, uploadID string, parts []CompletedPart) error
	AbortMultipartUpload(ctx context.Context, objectKey, uploadID string) error
}

// MinioStore implements ObjectStore with MinIO.
type MinioStore struct {
	client *minio.Client
	core   minio.Core
	bucket string
}

// NewMinioStore connects to MinIO and ensures the bucket exists.
func NewMinioStore(cfg config.Config) (*MinioStore, error) {
	client, err := minio.New(cfg.MinioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinioAccessKey, cfg.MinioSecretKey, ""),
		Secure: cfg.MinioUseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}

	ctx := context.Background()
	exists, err := client.BucketExists(ctx, cfg.MinioBucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.MinioBucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("create bucket: %w", err)
		}
	}

	return &MinioStore{
		client: client,
		core:   minio.Core{Client: client},
		bucket: cfg.MinioBucket,
	}, nil
}

// PutObject uploads a single object.
func (s *MinioStore) PutObject(ctx context.Context, objectKey, contentType string, reader io.Reader, size int64) error {
	_, err := s.client.PutObject(ctx, s.bucket, objectKey, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	return err
}

// RemoveObject deletes an object.
func (s *MinioStore) RemoveObject(ctx context.Context, objectKey string) error {
	return s.client.RemoveObject(ctx, s.bucket, objectKey, minio.RemoveObjectOptions{})
}

// StatObject returns object metadata.
func (s *MinioStore) StatObject(ctx context.Context, objectKey string) (ObjectInfo, error) {
	info, err := s.client.StatObject(ctx, s.bucket, objectKey, minio.StatObjectOptions{})
	if err != nil {
		return ObjectInfo{}, err
	}
	contentType := info.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return ObjectInfo{
		SizeBytes:   info.Size,
		ContentType: contentType,
	}, nil
}

// GetObject reads object bytes. When length is negative, the full object is returned.
func (s *MinioStore) GetObject(ctx context.Context, objectKey string, offset, length int64) (io.ReadCloser, error) {
	opts := minio.GetObjectOptions{}
	if length >= 0 {
		end := offset + length - 1
		if err := opts.SetRange(offset, end); err != nil {
			return nil, err
		}
	}
	return s.client.GetObject(ctx, s.bucket, objectKey, opts)
}

// CreateMultipartUpload starts a multipart upload session.
func (s *MinioStore) CreateMultipartUpload(ctx context.Context, objectKey, contentType string) (string, error) {
	uploadID, err := s.core.NewMultipartUpload(ctx, s.bucket, objectKey, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", err
	}
	return uploadID, nil
}

// PresignUploadParts returns presigned PUT URLs for each part.
func (s *MinioStore) PresignUploadParts(ctx context.Context, objectKey, uploadID string, partCount int, expiry time.Duration) ([]PresignedPart, error) {
	parts := make([]PresignedPart, 0, partCount)
	for partNumber := 1; partNumber <= partCount; partNumber++ {
		reqParams := url.Values{}
		reqParams.Set("uploadId", uploadID)
		reqParams.Set("partNumber", strconv.Itoa(partNumber))

		presignedURL, err := s.client.Presign(ctx, http.MethodPut, s.bucket, objectKey, expiry, reqParams)
		if err != nil {
			return nil, err
		}
		parts = append(parts, PresignedPart{
			PartNumber: partNumber,
			UploadURL:  presignedURL.String(),
		})
	}
	return parts, nil
}

// CompleteMultipartUpload merges uploaded parts.
func (s *MinioStore) CompleteMultipartUpload(ctx context.Context, objectKey, uploadID string, parts []CompletedPart) error {
	minioParts := make([]minio.CompletePart, 0, len(parts))
	for _, part := range parts {
		minioParts = append(minioParts, minio.CompletePart{
			PartNumber: part.PartNumber,
			ETag:       part.ETag,
		})
	}
	_, err := s.core.CompleteMultipartUpload(ctx, s.bucket, objectKey, uploadID, minioParts, minio.PutObjectOptions{})
	return err
}

// AbortMultipartUpload cancels a multipart upload.
func (s *MinioStore) AbortMultipartUpload(ctx context.Context, objectKey, uploadID string) error {
	return s.core.AbortMultipartUpload(ctx, s.bucket, objectKey, uploadID)
}
