package inbox

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractDocx(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	w, err := zw.Create("word/document.xml")
	require.NoError(t, err)
	_, err = w.Write([]byte(`<?xml version="1.0"?><w:document><w:body><w:p><w:r><w:t>Hello</w:t></w:r><w:r><w:t>World</w:t></w:r></w:p></w:body></w:document>`))
	require.NoError(t, err)
	zw.Close()
	text, err := ExtractText("test.docx", buf.Bytes())
	require.NoError(t, err)
	assert.Contains(t, text, "Hello")
	assert.Contains(t, text, "World")
}

func TestExtractTxt(t *testing.T) {
	text, err := ExtractText("a.txt", []byte("hello world"))
	require.NoError(t, err)
	assert.Equal(t, "hello world", text)
}

func TestExtractTooLarge(t *testing.T) {
	large := make([]byte, MaxUpload+1)
	_, err := ExtractText("big.txt", large)
	assert.Error(t, err)
}

func TestExtractPdf_RealTextLayer(t *testing.T) {
	// 注意：ledongthuc/pdf 的 GetPlainText 解释器遇未知操作符即中止，
	// 故构造流仅含 BT / Tf / Tj / ET（不含 Td 等定位算子）
	pdf := buildMinimalPDF("(Hello FORJA requirement) Tj\n(Forged by spec) Tj")
	text, err := ExtractText("a.pdf", pdf)
	require.NoError(t, err, "合法文本层 PDF 应可抽取")
	assert.Contains(t, text, "Hello FORJA requirement")
	assert.Contains(t, text, "Forged by spec")
}

func TestExtractPdf_FlateCompressed(t *testing.T) {
	// 内容流经 FlateDecode 压缩（真实世界 PDF 的主流形态）
	var comp bytes.Buffer
	zw := zlib.NewWriter(&comp)
	_, err := zw.Write([]byte("BT\n(compressed forja requirement text) Tj\nET"))
	require.NoError(t, err)
	zw.Close()

	stream := comp.Bytes()
	objs := []struct{ head string; body []byte }{
		{"<< /Type /Catalog /Pages 2 0 R >>", nil},
		{"<< /Type /Pages /Kids [3 0 R] /Count 1 >>", nil},
		{"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R >>", nil},
		{fmt.Sprintf("<< /Length %d /Filter /FlateDecode >>", len(stream)), stream},
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s", i+1, o.head)
		if o.body != nil {
			fmt.Fprintf(&buf, "\nstream\n%s\nendstream", o.body)
		}
		fmt.Fprintf(&buf, "\nendobj\n")
	}
	xrefAt := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xrefAt)

	text, err := ExtractText("c.pdf", buf.Bytes())
	require.NoError(t, err)
	assert.Contains(t, text, "compressed forja requirement text")
}

func TestExtractPdf_GarbageRejected(t *testing.T) {
	// 非法字节：真抽取应报错而非占位透传
	_, err := ExtractText("a.pdf", []byte("%PDF-1.4\n1 0 obj\n<<>>\nstream\n(Hello World)\nendstream\nendobj\n"))
	assert.Error(t, err, "无合法 xref/页树的伪 PDF 应被拒绝")
}

// buildMinimalPDF 程序化构建带正确 xref 偏移的最小单页 PDF（Helvetica 文本两行）
func buildMinimalPDF(textStream string) []byte {
	header := "%PDF-1.4\n"
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\nBT /F1 14 Tf 72 720 Td %s ET", len(textStream), textStream) + "\nendstream",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var buf bytes.Buffer
	buf.WriteString(header)
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xrefAt := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xrefAt)
	return buf.Bytes()
}

func TestExtractPdf_Empty(t *testing.T) {
	_, err := ExtractText("a.pdf", []byte{0, 1, 2, 3})
	assert.Error(t, err)
}

func TestExtractDocx_MissingDocumentXml(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	w, err := zw.Create("other.xml")
	require.NoError(t, err)
	_, _ = w.Write([]byte("x"))
	zw.Close()
	_, err = ExtractText("test.docx", buf.Bytes())
	assert.Error(t, err)
}

func TestExtractMd(t *testing.T) {
	text, err := ExtractText("readme.md", []byte("# Title\ncontent"))
	require.NoError(t, err)
	assert.Equal(t, "# Title\ncontent", text)
}
