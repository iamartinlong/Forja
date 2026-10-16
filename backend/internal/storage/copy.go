package storage

import (
	"context"
	"fmt"
	"io"
)

// Copy 迁移工具：在两个 FileStore 之间拷贝对象（storage copy --from --to 可逆）。
// 遍历由调用方提供 key 列表（通常从 DB inbox_attachments / project_files 全量查询）。
func Copy(ctx context.Context, from, to FileStore, keys []string) (copied int, errs []error) {
	for _, key := range keys {
		if err := CopyOne(ctx, from, to, key); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
			continue
		}
		copied++
	}
	return copied, errs
}

// CopyOne 拷贝单个 key。
func CopyOne(ctx context.Context, from, to FileStore, key string) error {
	rc, err := from.Open(ctx, key)
	if err != nil {
		return fmt.Errorf("open from: %w", err)
	}
	defer rc.Close()

	// 尝试探测大小（本地可通过 Stat，这里简化为流式 -1）
	if err := to.Put(ctx, key, rc, -1, ""); err != nil {
		return fmt.Errorf("put to: %w", err)
	}
	return nil
}

// EnsureCopyReader 辅助：若 FileStore.Open 返回的流需完整缓冲，可在此扩展。

// CopyWithReader 供测试或自定义 Reader 的拷贝。
func CopyWithReader(ctx context.Context, to FileStore, key string, r io.Reader, size int64, mime string) error {
	return to.Put(ctx, key, r, size, mime)
}
