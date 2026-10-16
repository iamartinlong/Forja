package auth

// S1.2 Refresh Token 单元测试：AES-GCM 往返/篡改检出/TTL 配置（不依赖 DB）。

import (
	"bytes"
	crand "crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAESGCM_RoundTripAndTamper(t *testing.T) {
	aead, err := aesGCM()
	require.NoError(t, err)

	raw := "test-token-bytes"
	nonce := make([]byte, aead.NonceSize())
	_, err = crand.Read(nonce)
	require.NoError(t, err)
	ct := aead.Seal(nonce, nonce, []byte(raw), nil)

	// 正确解密
	plain, err := aead.Open(nil, ct[:aead.NonceSize()], ct[aead.NonceSize():], nil)
	require.NoError(t, err)
	assert.Equal(t, raw, string(plain))

	// 篡改密文必须失败
	bad := append([]byte{}, ct...)
	bad[len(bad)-1] ^= 0x01
	_, err = aead.Open(nil, bad[:aead.NonceSize()], bad[aead.NonceSize():], nil)
	assert.Error(t, err, "被篡改的密文必须解密失败")

	// 密钥派生稳定：两次构建的 AEAD 可互解
	aead2, err := aesGCM()
	require.NoError(t, err)
	plain2, err := aead2.Open(nil, ct[:aead.NonceSize()], ct[aead.NonceSize():], nil)
	require.NoError(t, err)
	assert.True(t, bytes.Equal([]byte(raw), plain2))
}

func TestHashToken_StableHex(t *testing.T) {
	h1 := hashToken("same-input")
	h2 := hashToken("same-input")
	h3 := hashToken("other")
	assert.Len(t, h1, 64)
	assert.Equal(t, h1, h2)
	assert.NotEqual(t, h1, h3)
}

func TestGenerateRawToken_Format(t *testing.T) {
	a, err := GenerateRawToken()
	require.NoError(t, err)
	b, err := GenerateRawToken()
	require.NoError(t, err)
	assert.Len(t, a, 43) // 32 bytes base64url 无填充
	assert.NotEqual(t, a, b)
}

func TestTTLFromEnv(t *testing.T) {
	t.Setenv("FORJA_REFRESH_TTL_HOURS", "3")
	assert.Equal(t, 3*3600*1000_000_000, int(RefreshTTL()))
	t.Setenv("FORJA_ACCESS_TOKEN_TTL_MINUTES", "30")
	assert.Equal(t, 30*60*1000_000_000, int(AccessTokenTTL()))
}
