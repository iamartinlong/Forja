package validation

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunGoVet_TempDir_Pass(t *testing.T) {
	dir := t.TempDir()
	// minimal go.mod + main.go that passes vet
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module tmp\n\ngo 1.22\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){}\n"), 0644))
	t.Setenv("VET_TEST_DIR", dir)
	t.Setenv("SKIP_VET", "")
	ok, out := runGoVet(context.Background(), "", nil)
	assert.True(t, ok, "vet should pass: %s", out)
}

func TestRunGoVet_TempDir_Fail(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module tmp\n\ngo 1.22\n"), 0644))
	// vet should fail: printf with wrong verb
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nimport \"fmt\"\nfunc main(){ fmt.Printf(\"%d\", \"string\") }\n"), 0644))
	t.Setenv("VET_TEST_DIR", dir)
	t.Setenv("SKIP_VET", "")
	ok, out := runGoVet(context.Background(), "", nil)
	assert.False(t, ok, "vet should fail: %s", out)
	assert.NotEmpty(t, out)
}

func TestRunGoTest_TempDir_NoTests(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module tmp\n\ngo 1.22\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){}\n"), 0644))
	t.Setenv("VET_TEST_DIR", dir)
	t.Setenv("SKIP_VET", "")
	ok, out, cov := runGoTest(context.Background(), "", nil)
	// no test files -> should be true (handled as pass) or skipped
	assert.True(t, ok, "go test with no files should pass: %s", out)
	assert.Equal(t, float64(-1), cov, "no test files should yield no coverage")
}
