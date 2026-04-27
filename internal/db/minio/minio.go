package minio

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Client wraps a MinIO client for object storage.
type Client struct {
	mc     *minio.Client
	bucket string
}

// Config holds MinIO connection parameters.
type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	UseSSL    bool
}

// NewClient creates a new MinIO client, ensures the bucket exists.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	mc, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio: create client: %w", err)
	}

	exists, err := mc.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("minio: bucket exists check: %w", err)
	}
	if !exists {
		if err := mc.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("minio: create bucket: %w", err)
		}
	}

	return &Client{mc: mc, bucket: cfg.Bucket}, nil
}

// PutObject uploads data to the given object name.
func (c *Client) PutObject(
	ctx context.Context,
	objectName string,
	reader io.Reader,
	size int64,
	contentType string,
) error {
	_, err := c.mc.PutObject(ctx, c.bucket, objectName, reader, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("minio: put object: %w", err)
	}
	return nil
}

// GetObject downloads the data for the given object name.
// The caller must close the returned reader.
func (c *Client) GetObject(ctx context.Context, objectName string) (io.ReadCloser, error) {
	obj, err := c.mc.GetObject(ctx, c.bucket, objectName, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("minio: get object: %w", err)
	}
	return obj, nil
}

// DeleteObject removes the given object.
func (c *Client) DeleteObject(ctx context.Context, objectName string) error {
	return c.mc.RemoveObject(ctx, c.bucket, objectName, minio.RemoveObjectOptions{})
}

// HealthCheck verifies the bucket is accessible.
func (c *Client) HealthCheck(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.mc.BucketExists(checkCtx, c.bucket)
	return err
}
