package validation

import (
	"os"
	"path/filepath"
	"testing"
)

// v2.2 T1：多栈依赖文件探测（node: package-lock.json / python: requirements*.txt）。
func TestHasDepsFilePerStack(t *testing.T) {
	dir := t.TempDir()
	if hasDepsFile(dir, "python") {
		t.Fatal("empty dir should not have python deps")
	}
	if hasDepsFile(dir, "node") {
		t.Fatal("empty dir should not have node lock file")
	}
	if err := os.WriteFile(filepath.Join(dir, "requirements-dev.txt"), []byte("pytest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasDepsFile(dir, "python") {
		t.Fatal("requirements-dev.txt should mark python deps present")
	}
	if hasDepsFile(dir, "node") {
		t.Fatal("python deps must not satisfy node lock check")
	}
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasDepsFile(dir, "node") {
		t.Fatal("package-lock.json should mark node deps present")
	}

	// requirements.txt（无 dev 文件）同样命中
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "requirements.txt"), []byte("fastapi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasDepsFile(dir2, "python") {
		t.Fatal("requirements.txt should mark python deps present")
	}
}
