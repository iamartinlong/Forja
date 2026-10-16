package inbox

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	readpdf "github.com/ledongthuc/pdf"
)

const MaxUpload = 5 * 1024 * 1024 // 5MB per inbox-ingest.md

// ExtractText from filename + data; supports .txt/.md/.docx/.pdf (pdf as placeholder)
func ExtractText(filename string, data []byte) (string, error) {
	if len(data) > MaxUpload {
		return "", fmt.Errorf("file too large >5MB")
	}
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".txt", ".md", "":
		return string(data), nil
	case ".docx":
		return extractDocx(data)
	case ".pdf":
		return extractPdf(data)
	default:
		// fallback as text
		return string(data), nil
	}
}

func extractDocx(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("docx zip: %w", err)
	}
	var docXML []byte
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			docXML, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return "", err
			}
			break
		}
	}
	if len(docXML) == 0 {
		return "", fmt.Errorf("word/document.xml not found")
	}
	// strip XML tags, keep w:t text
	re := regexp.MustCompile(`<w:t[^>]*>([^<]*)</w:t>`)
	matches := re.FindAllStringSubmatch(string(docXML), -1)
	var sb strings.Builder
	for _, m := range matches {
		if len(m) > 1 {
			sb.WriteString(m[1])
			sb.WriteString(" ")
		}
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		// fallback strip all tags
		re2 := regexp.MustCompile(`<[^>]+>`)
		text = strings.TrimSpace(re2.ReplaceAllString(string(docXML), " "))
	}
	return text, nil
}

// extractPdf 真实文本抽取（S2/B1，替换占位）。两级策略：
//  1. ledongthuc/pdf 结构化解析（对 TJ 风格内容流效果好）
//  2. 兜底：扫描全部 stream 块（zlib 解压后），按括号配平提取 Tj/TJ 字符串字面量——
//     不受操作符顺序（Td/Tm 等）影响
//
// 扫描件（无文本层）返回明确错误；库内 panic 一并捕获转错误。
func extractPdf(data []byte) (string, error) {
	// ---- 策略 1：结构化解析 ----
	var text string
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				text, err = "", fmt.Errorf("pdf parse failed: %v", p)
			}
		}()
		r, e := readpdf.NewReader(bytes.NewReader(data), int64(len(data)))
		if e != nil {
			err = fmt.Errorf("pdf open: %w", e)
			return
		}
		rd, e := r.GetPlainText()
		if e != nil {
			err = e
			return
		}
		b, e := io.ReadAll(rd)
		if e == nil {
			text = string(b)
		}
	}()
	if joined := strings.Join(strings.Fields(text), " "); len([]rune(joined)) >= 10 {
		return joined, nil
	}

	// ---- 策略 2：内容流字面量扫描 ----
	raw := pdfScanLiterals(data)
	if joined := strings.Join(strings.Fields(raw), " "); len([]rune(joined)) >= 10 {
		return joined, nil
	}
	if err != nil {
		return "", fmt.Errorf("pdf 无可抽取文本层（%v；可能为扫描件，请改用 docx/txt 或先 OCR）", err)
	}
	return "", fmt.Errorf("pdf 无可抽取文本层（可能为扫描件），请改用 docx/txt 或先 OCR")
}

// pdfScanLiterals 提取所有 stream 块中的 PDF 字符串字面量：
// 解压 FlateDecode（compress/zlib），跳过二进制块，括号配平处理转义。
func pdfScanLiterals(data []byte) string {
	var out strings.Builder
	blocks := pdfStreamBlocks(data)
	for _, blk := range blocks {
		content := blk
		if dec, zerr := zlibDecompress(blk); zerr == nil && len(dec) > 0 {
			content = dec
		}
		if !bytes.Contains(content, []byte("Tj")) && !bytes.Contains(content, []byte("TJ")) &&
			!bytes.Contains(content, []byte("BT")) {
			continue // 非文本内容流（图片/字体等）
		}
		out.WriteString(pdfLiterals(content))
		out.WriteString(" ")
	}
	return strings.TrimSpace(out.String())
}

// pdfStreamBlocks 返回所有 stream...endstream 载荷
func pdfStreamBlocks(data []byte) [][]byte {
	var res [][]byte
	marker := []byte("stream")
	end := []byte("endstream")
	for i := 0; i+len(marker) <= len(data); {
		s := bytes.Index(data[i:], marker)
		if s < 0 {
			break
		}
		s += i + len(marker)
		// 跳过 stream 后的 EOL
		if s < len(data) && data[s] == '\r' {
			s++
		}
		if s < len(data) && data[s] == '\n' {
			s++
		}
		e := bytes.Index(data[s:], end)
		if e < 0 {
			break
		}
		payload := data[s : s+e]
		// 去掉载荷尾部 EOL
		payload = bytes.TrimSuffix(payload, []byte("\n"))
		payload = bytes.TrimSuffix(payload, []byte("\r"))
		res = append(res, payload)
		i = s + e + len(end)
	}
	return res
}

func zlibDecompress(b []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(io.LimitReader(zr, MaxUpload*4))
}

// pdfLiterals 从内容流中按括号配平提取字符串字面量（处理 \(\)\\ 等转义）
func pdfLiterals(content []byte) string {
	var sb strings.Builder
	depth := 0
	inStr := false
	var cur strings.Builder
	for i := 0; i < len(content); i++ {
		ch := content[i]
		if !inStr {
			if ch == '(' {
				inStr, depth, cur = true, 1, strings.Builder{}
			}
			continue
		}
		switch ch {
		case '\\':
			if i+1 < len(content) {
				i++
				switch content[i] {
				case 'n':
					cur.WriteByte('\n')
				case 't':
					cur.WriteByte('\t')
				case 'r':
					cur.WriteByte('\r')
				default:
					cur.WriteByte(content[i]) // \( \) \\ 及八进制简化取原字符
				}
			}
		case '(':
			depth++
			cur.WriteByte('(')
		case ')':
			depth--
			if depth == 0 {
				inStr = false
				sb.WriteString(cur.String())
				sb.WriteString(" ")
			} else {
				cur.WriteByte(')')
			}
		default:
			cur.WriteByte(ch)
		}
	}
	return sb.String()
}
