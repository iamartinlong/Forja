package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalDriver 本地文件系统驱动（默认，零依赖）。
// root 为宿主绝对路径（D:/opencode/SuperPM/uploads）或容器内 /app/uploads。
type LocalDriver struct {
	root string
}

// NewLocal 创建本地驱动，root 不存在则创建。
func NewLocal(root string) (*LocalDriver, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("local root empty")
	}
	// 清理路径
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir local root %q: %w", root, err)
	}
	return &LocalDriver{root: root}, nil
}

// Root 暴露根路径（测试/诊断用）。
func (l *LocalDriver) Root() string { return l.root }

func (l *LocalDriver) resolve(key string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("empty key")
	}
	// 防止路径穿越
	clean := filepath.Clean(key)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") || strings.Contains(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid key %q", key)
	}
	// 统一用 / 分隔的 key 转为本地分隔符
	clean = filepath.FromSlash(clean)
	abs := filepath.Join(l.root, clean)
	// 二次校验：abs 必须在 root 内
	rel, err := filepath.Rel(l.root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("key escapes root: %q", key)
	}
	return abs, nil
}

func (l *LocalDriver) Put(ctx context.Context, key string, r io.Reader, size int64, mime string) error {
	abs, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	// 原子写入：先写临时文件再重命名
	tmp := abs + ".tmp." + fmt.Sprint(time.Now().UnixNano())
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	n, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("copy: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close tmp: %w", closeErr)
	}
	if size > 0 && n != size {
		// 大小不一致仅告警，不阻断（分块/压缩场景）
	}
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func (l *LocalDriver) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	abs, err := l.resolve(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("not found: %w", err)
		}
		return nil, err
	}
	return f, nil
}

func (l *LocalDriver) Delete(ctx context.Context, key string) error {
	abs, err := l.resolve(key)
	if err != nil {
		return err
	}
	err = os.Remove(abs)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// 尝试清理空的 yyyymm / projectID 目录（可选，不报错）
	_ = tryRemoveEmptyParent(filepath.Dir(abs), l.root)
	return nil
}

func tryRemoveEmptyParent(dir, root string) error {
	for dir != root && dir != "." && dir != "/" {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			break
		}
		_ = os.Remove(dir)
		dir = filepath.Dir(dir)
	}
	return nil
}

func (l *LocalDriver) Presign(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > 5*time.Minute {
		return "", fmt.Errorf("presign ttl must be (0,5m], got %v", ttl)
	}
	// 本地驱动不支持预签名（走后端透传）；返回错误让调用方回退到 Open 流式
	return "", fmt.Errorf("presign not supported for local driver (use Open streaming)")
}
