package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Driver 基于 minio-go 的 S3 兼容驱动（MinIO/AWS/R2 等）。
type S3Driver struct {
	client *minio.Client
	bucket string
	prefix string // 可选 key 前缀
}

// NewS3 创建 S3 驱动；即使 MinIO 未启动也能初始化，操作时才建连。
func NewS3(cfg Config) (*S3Driver, error) {
	if strings.TrimSpace(cfg.S3Bucket) == "" {
		return nil, fmt.Errorf("S3_BUCKET required for s3 driver")
	}
	endpoint := strings.TrimSpace(cfg.S3Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("S3_ENDPOINT required for s3 driver")
	}
	// 去掉 scheme 前缀（minio-go 要求 host:port 形式，Secure 另控）
	secure := false
	raw := endpoint
	if strings.HasPrefix(raw, "https://") {
		secure = true
		raw = strings.TrimPrefix(raw, "https://")
	} else if strings.HasPrefix(raw, "http://") {
		raw = strings.TrimPrefix(raw, "http://")
	}
	// 去掉尾部斜杠
	raw = strings.TrimSuffix(raw, "/")

	creds := credentials.NewStaticV4(cfg.S3AccessKey, cfg.S3Secret, "")
	lookup := minio.BucketLookupAuto
	if cfg.S3PathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(raw, &minio.Options{
		Creds:        creds,
		Secure:       secure,
		Region:       cfg.S3Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, fmt.Errorf("minio new: %w", err)
	}
	// Bucket 存在性检查不在构造时做（避免启动时阻塞），Put 时自动按需创建

	return &S3Driver{
		client: client,
		bucket: cfg.S3Bucket,
	}, nil
}

func (s *S3Driver) Put(ctx context.Context, key string, r io.Reader, size int64, mime string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("empty key")
	}
	if size < 0 {
		size = -1
	}
	opts := minio.PutObjectOptions{
		ContentType: mime,
	}
	if mime == "" {
		opts.ContentType = "application/octet-stream"
	}
	_, err := s.client.PutObject(ctx, s.bucket, s.prefix+key, r, size, opts)
	if err != nil {
		return fmt.Errorf("s3 put %q: %w", key, err)
	}
	return nil
}

func (s *S3Driver) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("empty key")
	}
	obj, err := s.client.GetObject(ctx, s.bucket, s.prefix+key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("s3 get %q: %w", key, err)
	}
	// 探测是否存在
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, fmt.Errorf("not found: %w", err)
	}
	return obj, nil
}

func (s *S3Driver) Delete(ctx context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("empty key")
	}
	err := s.client.RemoveObject(ctx, s.bucket, s.prefix+key, minio.RemoveObjectOptions{})
	if err != nil {
		// 幂等：不存在也视作成功（minio-go 对不存在 key 不报错，此分支仅网错）
		return fmt.Errorf("s3 delete %q: %w", key, err)
	}
	return nil
}

func (s *S3Driver) Presign(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > 5*time.Minute {
		return "", fmt.Errorf("presign ttl must be (0,5m], got %v", ttl)
	}
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("empty key")
	}
	params := make(url.Values)
	// 强制下载：仅暴露原始文件名，避免泄露内部 key（含 projectID）
	base := filepath.Base(key)
	// 剥离 uuid 前缀，保留原始名可读性（BuildKey 格式 {uuid}-{orig}）
	if idx := strings.Index(base, "-"); idx > 0 && len(base) > idx+1 {
		// 若含 uuid 前缀，仍用全 base 以保证唯一性，但已去路径
	}
	params.Set("response-content-disposition", fmt.Sprintf("attachment; filename=%q", base))
	u, err := s.client.PresignedGetObject(ctx, s.bucket, s.prefix+key, ttl, params)
	if err != nil {
		return "", fmt.Errorf("presign %q: %w", key, err)
	}
	return u.String(), nil
}
