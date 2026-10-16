package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocal_PutOpenDelete(t *testing.T) {
	dir := t.TempDir()
	s, err := NewLocal(dir)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	ctx := context.Background()
	key := BuildKey("inbox", "00000000-0000-0000-0000-000000000001", "hello.txt")
	data := []byte("hello storage")
	if err := s.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// 物理路径存在
	abs := filepath.Join(dir, filepath.FromSlash(key))
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("stat after put: %v", err)
	}
	rc, err := s.Open(ctx, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("mismatch: got %q want %q", string(got), string(data))
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Fatalf("expected not exist after delete, got %v", err)
	}
	// 幂等删除
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("second Delete should be idempotent: %v", err)
	}
	// 打开不存在
	if _, err := s.Open(ctx, key); err == nil {
		t.Fatalf("expected error opening missing key")
	}
}

func TestLocal_PresignNotSupported(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewLocal(dir)
	_, err := s.Presign(context.Background(), "x/y/z.txt", 2*time.Minute)
	if err == nil || !contains(err.Error(), "not supported") {
		t.Fatalf("expected not supported, got %v", err)
	}
	_, err = s.Presign(context.Background(), "x/y/z.txt", 10*time.Minute)
	if err == nil {
		t.Fatalf("expected ttl too long error")
	}
}

func TestLocal_ValidateAndKey(t *testing.T) {
	if err := ValidateFile("a.pdf", 100); err != nil {
		t.Fatalf("pdf should pass: %v", err)
	}
	if err := ValidateFile("a.exe", 100); err == nil {
		t.Fatalf("exe should be rejected")
	}
	if err := ValidateFile("a.pdf", MaxFileSize+1); err == nil {
		t.Fatalf("too large should be rejected")
	}
	k := BuildKey("project", "pid-123", "brand 手册.pdf")
	if !contains(k, "project/pid-123/") {
		t.Fatalf("key scope/project mismatch: %s", k)
	}
	if !contains(k, "brand") {
		t.Fatalf("original name should be preserved: %s", k)
	}
	// 路径穿越防护
	dir := t.TempDir()
	s, _ := NewLocal(dir)
	if err := s.Put(context.Background(), "../escape.txt", bytes.NewReader([]byte("x")), 1, ""); err == nil {
		t.Fatalf("should reject path traversal")
	}
}

func TestLocal_BuildKeyFormat(t *testing.T) {
	k := BuildKey("inbox", "proj-1", "a.docx")
	parts := filepath.ToSlash(k)
	// 期望 4 段：scope/project/yyyymm/uuid-name
	// 简单校验：以 inbox/proj-1/ 开头且包含 .docx
	if !contains(parts, "inbox/proj-1/") || !contains(parts, ".docx") {
		t.Fatalf("unexpected key %s", k)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i <= len(s)-len(substr); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}
