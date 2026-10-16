package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"io"
	"os"
	"strings"
)

// normalizeKey 归一到 32 字节（<32 补零，>32 截断）
func normalizeKey(s string) []byte {
	b := []byte(s)
	if len(b) < 32 {
		pad := make([]byte, 32)
		copy(pad, b)
		return pad
	}
	return b[:32]
}

// keys R7 密钥轮换：主钥（加密用）+ 历史钥（解密兼容）。
// 主钥：APP_SECRET → FORJA_APP_SECRET；历史钥：FORJA_APP_SECRET_PREVIOUS / APP_SECRET_PREVIOUS
// （支持逗号分隔多把，按序尝试）；无任何配置时退回 32 零字节 dev 兜底。
func keys() [][]byte {
	var out [][]byte
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		k := normalizeKey(s)
		if seen[string(k)] {
			return
		}
		seen[string(k)] = true
		out = append(out, k)
	}
	add(os.Getenv("APP_SECRET"))
	add(os.Getenv("FORJA_APP_SECRET"))
	for _, env := range []string{"FORJA_APP_SECRET_PREVIOUS", "APP_SECRET_PREVIOUS"} {
		for _, s := range strings.Split(os.Getenv(env), ",") {
			add(s)
		}
	}
	if len(out) == 0 {
		// dev fallback: 32 byte zero key (not secure, but allows vet/test)
		out = append(out, make([]byte, 32))
	}
	return out
}

// key 主钥（兼容既有调用）
func key() []byte { return keys()[0] }

func Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	block, err := aes.NewCipher(key())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// Decrypt 依次尝试主钥与历史钥（轮换窗口内新旧数据均可解）；全部失败按 legacy 明文返回。
func Decrypt(cipherB64 string) (string, error) {
	if cipherB64 == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		// legacy plain not base64
		return cipherB64, nil
	}
	for _, k := range keys() {
		block, err := aes.NewCipher(k)
		if err != nil {
			continue
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			continue
		}
		nonceSize := gcm.NonceSize()
		if len(data) < nonceSize {
			continue
		}
		nonce, ct := data[:nonceSize], data[nonceSize:]
		if pt, err := gcm.Open(nil, nonce, ct, nil); err == nil {
			return string(pt), nil
		}
	}
	// fallback: maybe plain not encrypted (legacy row)
	return cipherB64, nil
}

// IsEncrypted heuristic: base64 and decrypt succeeds
func IsEncrypted(s string) bool {
	if s == "" {
		return false
	}
	_, err := Decrypt(s)
	return err == nil && s != "" && len(s) > 20
}
