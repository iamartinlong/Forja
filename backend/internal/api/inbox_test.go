package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"forja/backend/internal/api/respond"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	"forja/backend/internal/inbox"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupInboxApp(t *testing.T) *fiber.App {
	t.Helper()
	ensureTestDB(t)
	app := fiber.New(fiber.Config{ErrorHandler: respond.HandleError})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("username", "test")
		c.Locals("role", "Admin")
		return c.Next()
	})
	api := app.Group("/api/v1")
	RegisterInboxRoutes(api)
	return app
}

func TestInbox_CreateMultipart_Docx(t *testing.T) {
	app := setupInboxApp(t)
	// build docx zip
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	w, _ := zw.Create("word/document.xml")
	_, _ = w.Write([]byte(`<?xml version="1.0"?><w:document><w:body><w:p><w:r><w:t>Multipart Hello</w:t></w:r></w:p></w:body></w:document>`))
	zw.Close()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, _ := mw.CreateFormFile("file", "test.docx")
	_, _ = fw.Write(buf.Bytes())
	fp, _ := mw.CreateFormField("project_id")
	_, _ = fp.Write([]byte("00000000-0000-0000-0000-000000000001"))
	mw.Close()
	req := httptest.NewRequest("POST", "/api/v1/inbox", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	outData, _ := out["data"].(map[string]any)
	id, _ := outData["id"].(string)
	assert.NotEmpty(t, id)
	// verify raw_content was extracted
	var raw string
	_ = model.Pool.QueryRow(context.Background(), `SELECT raw_content FROM requirements_inbox WHERE id=$1`, id).Scan(&raw)
	assert.Contains(t, raw, "Multipart Hello")
	// cleanup
	_, _ = model.Pool.Exec(context.Background(), `DELETE FROM requirements_inbox WHERE id=$1`, id)
	_ = uuid.NewString
}

func TestInbox_CreateMultipart_TooLarge(t *testing.T) {
	// direct parser test for too large
	large := make([]byte, 6*1024*1024)
	_, err := inbox.ExtractText("big.txt", large)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too large")
}

func TestInbox_UpdateAndDelete(t *testing.T) {
	app := setupInboxApp(t)
	ctx := context.Background()
	id := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, content_hash, status) VALUES ($1,'00000000-0000-0000-0000-000000000001','TEXT_PASTE','delete me','hash-del-test','NEW')`, id)
	require.NoError(t, err)

	// PATCH update
	body := bytes.NewReader([]byte(`{"raw_content":"updated content"}`))
	req := httptest.NewRequest("PATCH", "/api/v1/inbox/"+id, body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.StatusCode)
	var raw string
	_ = model.Pool.QueryRow(ctx, `SELECT raw_content FROM requirements_inbox WHERE id=$1`, id).Scan(&raw)
	assert.Equal(t, "updated content", raw)

	// DELETE
	req2 := httptest.NewRequest("DELETE", "/api/v1/inbox/"+id, nil)
	resp2, err := app.Test(req2, 5000)
	require.NoError(t, err)
	assert.Equal(t, 200, resp2.StatusCode)
	var n int
	_ = model.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM requirements_inbox WHERE id=$1`, id).Scan(&n)
	assert.Equal(t, 0, n)
}

func TestInbox_DeleteConverted_Forbidden(t *testing.T) {
	app := setupInboxApp(t)
	ctx := context.Background()
	id := uuid.NewString()
	_, err := model.Pool.Exec(ctx, `INSERT INTO requirements_inbox (id, project_id, source_type, raw_content, content_hash, status) VALUES ($1,'00000000-0000-0000-0000-000000000001','TEXT_PASTE','converted','hash-conv-test','CONVERTED')`, id)
	require.NoError(t, err)
	defer func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM requirements_inbox WHERE id=$1`, id) }()

	req := httptest.NewRequest("DELETE", "/api/v1/inbox/"+id, nil)
	resp, err := app.Test(req, 5000)
	require.NoError(t, err)
	assert.NotEqual(t, 200, resp.StatusCode)
}
