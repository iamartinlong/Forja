package validation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRunPipeline_NoDB(t *testing.T) {
	// when Pool is nil, should return Passed true
	// we temporarily set Pool to nil if needed, but by default in test Pool is nil
	res := RunPipeline(context.Background(), "test-id", "", 1, nil, time.Now().Add(-time.Hour))
	assert.True(t, res.Passed)
	assert.Equal(t, 1, res.Attempt)
}

func TestHasGo(t *testing.T) {
	// just check function exists and returns bool
	_ = hasGo()
	assert.True(t, true)
}

func TestRunGoVet_Skipped(t *testing.T) {
	t.Setenv("SKIP_VET", "1")
	ok, out := runGoVet(nil, "", nil)
	assert.True(t, ok)
	assert.Equal(t, "skipped", out)
}

func TestRunGoTest_Skipped(t *testing.T) {
	t.Setenv("SKIP_VET", "1")
	ok, out, _ := runGoTest(nil, "", nil)
	assert.True(t, ok)
	assert.Equal(t, "skipped", out)
}

// TestRunGoTest_FailNotExcusedByNoTestFiles 2026-09-07 回归：任一包真实 FAIL 时，
// 另一包的 "[no test files]" 不得为整轮开脱（此前子串匹配直接放行，失败测试被判通过）。
func TestRunGoTest_FailNotExcusedByNoTestFiles(t *testing.T) {
	if !hasGo() {
		t.Skip("go toolchain not available")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module tmpfail\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgA := filepath.Join(dir, "a")
	pkgB := filepath.Join(dir, "b")
	if err := os.MkdirAll(pkgA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkgB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgA, "a_test.go"), []byte("package a\nimport \"testing\"\nfunc TestFail(t *testing.T){ t.Fatal(\"boom\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgB, "b.go"), []byte("package b\nfunc Hi(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, out, _ := runGoTest(context.Background(), dir, nil)
	assert.False(t, ok, "真实 FAIL 不得被 no-test-files 开脱，输出:\n%s", out)
}

// TestRunGoTidy 2026-09-08：无 go.mod 跳过；纯 stdlib 模块 tidy 通过；
// 坏 import（GOPROXY=off 秒级失败）判失败。
func TestRunGoTidy(t *testing.T) {
	if !hasGo() {
		t.Skip("go toolchain not available")
	}
	// 无 go.mod → 跳过通过
	ok, _ := runGoTidy(context.Background(), t.TempDir(), nil)
	assert.True(t, ok, "no go.mod should skip")

	t.Setenv("GOPROXY", "off")
	// 纯 stdlib → 通过
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module tidymod\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nimport \"fmt\"\nfunc main(){ fmt.Println(\"hi\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, _ = runGoTidy(context.Background(), dir, nil)
	assert.True(t, ok, "stdlib-only module tidy should pass")
	// 坏 import → 失败
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "go.mod"), []byte("module tidybad\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "main.go"), []byte("package main\nimport _ \"example.com/nonexistent/xyz\"\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, out := runGoTidy(context.Background(), dir2, nil)
	assert.False(t, ok, "unresolvable import tidy should fail")
	assert.NotEmpty(t, out)
}

// M5+ 门禁收紧回归：有 Go 源码但 0 个 _test.go 时，覆盖率门禁不得静默跳过。
func TestCountGoFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "handlers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "vendor", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "handlers", "h.go"), []byte("package handlers"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "vendor", "x", "v.go"), []byte("package x"), 0o644)
	src, tests := countGoFiles(dir)
	assert.Equal(t, 2, src, "vendor 下源码不应计入")
	assert.Equal(t, 0, tests)
	_ = os.WriteFile(filepath.Join(dir, "handlers", "h_test.go"), []byte("package handlers"), 0o644)
	src, tests = countGoFiles(dir)
	assert.Equal(t, 2, src)
	assert.Equal(t, 1, tests, "测试文件应被识别")
}

// Stage4 #2 瘦身回归：空模块目录（仅 go.mod 无源码）下 vet/test 必须放行——
// worker 镜像回退链命中 /app 时的门禁语义（go 1.24 对 matched no packages 返回 exit 1）
func TestRunGo_EmptyModuleFallback(t *testing.T) {
	if !hasGo() {
		t.Skip("go toolchain not available")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module slim.fallback\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	okVet, _ := runGoVet(context.Background(), dir, nil)
	assert.True(t, okVet, "vet must pass on empty module")
	okTest, _, cov := runGoTest(context.Background(), dir, nil)
	assert.True(t, okTest, "test must pass on empty module")
	assert.Equal(t, float64(-1), cov, "coverage should be undetermined (-1)")
}
