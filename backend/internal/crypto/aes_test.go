package crypto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	t.Setenv("APP_SECRET", "0123456789abcdef0123456789abcdef")
	plain := "sk-test-123"
	enc, err := Encrypt(plain)
	require.NoError(t, err)
	assert.NotEqual(t, plain, enc)
	dec, err := Decrypt(enc)
	require.NoError(t, err)
	assert.Equal(t, plain, dec)
}

func TestDecrypt_LegacyPlainFallback(t *testing.T) {
	t.Setenv("APP_SECRET", "0123456789abcdef0123456789abcdef")
	plain := "legacy-plain-key"
	dec, err := Decrypt(plain)
	require.NoError(t, err)
	// legacy non-base64 should fallback to original
	assert.Equal(t, plain, dec)
}

func TestEncrypt_Empty(t *testing.T) {
	enc, err := Encrypt("")
	require.NoError(t, err)
	assert.Equal(t, "", enc)
	dec, err := Decrypt("")
	require.NoError(t, err)
	assert.Equal(t, "", dec)
}

// R7 密钥轮换：新主钥 + 历史钥并存时，旧数据可解、新数据用主钥加密。
func TestDecrypt_KeyRotationPreviousKey(t *testing.T) {
	oldKey := "old-secret-0123456789abcdef012345" // 32B
	t.Setenv("APP_SECRET", oldKey)
	t.Setenv("FORJA_APP_SECRET", "")
	t.Setenv("APP_SECRET_PREVIOUS", "")
	t.Setenv("FORJA_APP_SECRET_PREVIOUS", "")
	encOld, err := Encrypt("sk-old-rotation")
	require.NoError(t, err)

	// 轮换：新主钥 + 旧钥挂 PREVIOUS（多把逗号分隔也验证）
	t.Setenv("APP_SECRET", "new-secret-0123456789abcdef012345")
	t.Setenv("FORJA_APP_SECRET_PREVIOUS", oldKey+",another-old-key-000000000000000000")
	dec, err := Decrypt(encOld)
	require.NoError(t, err)
	assert.Equal(t, "sk-old-rotation", dec)

	// 新数据用主钥加密后仍可解
	encNew, err := Encrypt("sk-new")
	require.NoError(t, err)
	dec, err = Decrypt(encNew)
	require.NoError(t, err)
	assert.Equal(t, "sk-new", dec)

	// 无助钥的随机密文 → legacy 兜底返回原文（不 panic）
	t.Setenv("FORJA_APP_SECRET_PREVIOUS", "")
	dec, err = Decrypt(encOld)
	require.NoError(t, err)
	assert.NotEqual(t, "sk-old-rotation", dec) // 无法解密时按 legacy base64 原文返回
}
