package auth

// Refresh Token 体系（S1.2 / KI-005 Token 吊销）：
//
//   - 原始 token：32 字节随机数 base64url，仅下发给客户端，服务端不落明文
//   - token_hash：SHA-256(原始 token)，唯一索引供 O(1) 查找与吊销
//   - cipher：AES-256-GCM(原始 token)，密钥由 JWT_SECRET 派生 ——
//     即使 DB 泄露，攻击者也无法还原可用 token（满足 A3「AES-256 存储」基线）
//
// 语义：登录签发；刷新时轮换（旧 token 立即作废，重放即失效）；
// 禁用用户/改密时全量吊销该用户 refresh token，实现"禁用即踢出续期能力"。

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"forja/backend/internal/id"
	"forja/backend/internal/model"
)

// ErrRefreshInvalid 刷新凭证无效（未知/篡改/过期/已吊销/用户禁用），统一映射 401。
var ErrRefreshInvalid = errors.New("refresh token invalid")

const refreshKeyInfo = "forja:refresh:v1"

// RefreshTTL 刷新令牌有效期：env FORJA_REFRESH_TTL_HOURS，默认 168h（7 天）。
func RefreshTTL() time.Duration {
	if n, err := strconv.Atoi(os.Getenv("FORJA_REFRESH_TTL_HOURS")); err == nil && n > 0 {
		return time.Duration(n) * time.Hour
	}
	return 168 * time.Hour
}

// AccessTokenTTL 访问令牌有效期：env FORJA_ACCESS_TOKEN_TTL_MINUTES，默认 15min。
// 引入静默续期后缩短访问窗，将禁用用户的残余有效窗口从 2h 压缩到分钟级。
func AccessTokenTTL() time.Duration {
	if n, err := strconv.Atoi(os.Getenv("FORJA_ACCESS_TOKEN_TTL_MINUTES")); err == nil && n > 0 {
		return time.Duration(n) * time.Minute
	}
	return 15 * time.Minute
}

func aesGCM() (cipher.AEAD, error) {
	sum := sha256.Sum256([]byte(string(jwtSecret) + ":" + refreshKeyInfo))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// GenerateRawToken 生成原始 refresh token（base64url，无填充）。
func GenerateRawToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// IssueRefreshToken 为用户签发 refresh token 并落库（hash + AES-256-GCM 密文）。
func IssueRefreshToken(ctx context.Context, uid string) (string, error) {
	raw, err := GenerateRawToken()
	if err != nil {
		return "", err
	}
	aead, err := aesGCM()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	cipherText := aead.Seal(nonce, nonce, []byte(raw), nil)
	ttl := RefreshTTL()
	if _, err = model.Pool.Exec(ctx,
		`INSERT INTO refresh_tokens (id, user_id, token_hash, cipher, expires_at) VALUES ($1,$2,$3,$4,$5)`,
		id.New(), uid, hashToken(raw), cipherText, time.Now().Add(ttl)); err != nil {
		return "", err
	}
	// 顺带清理已过期行，防表无限增长
	_, _ = model.Pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE expires_at < now()`)
	return raw, nil
}

// verifyCipher 解密比对（常数时间），校验 cipher 与 raw 一致。
func verifyCipher(raw string) bool {
	aead, err := aesGCM()
	if err != nil {
		return false
	}
	rows, err := model.Pool.Query(context.Background(),
		`SELECT cipher FROM refresh_tokens WHERE token_hash=$1 LIMIT 1`, hashToken(raw))
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cipherText []byte
		if err := rows.Scan(&cipherText); err != nil || len(cipherText) < aead.NonceSize() {
			continue
		}
		nonce, body := cipherText[:aead.NonceSize()], cipherText[aead.NonceSize():]
		plain, err := aead.Open(nil, nonce, body, nil)
		if err == nil && subtle.ConstantTimeCompare(plain, []byte(raw)) == 1 {
			return true
		}
	}
	return false
}

// RotateRefreshToken 校验并轮换：旧 token 标记吊销后签发新对。
// 返回新 refresh token 与用户 id。任何失败统一 ErrRefreshInvalid。
func RotateRefreshToken(ctx context.Context, raw string) (newRaw, uid string, err error) {
	h := hashToken(raw)
	ct, err := model.Pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at=now(), last_used_at=now()
		 WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at > now()`, h)
	if err != nil {
		return "", "", fmt.Errorf("rotate query: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return "", "", ErrRefreshInvalid // 不存在/已吊销/已过期/并发重放
	}
	if !verifyCipher(raw) {
		// hash 命中但密文不符：疑似 hash 碰撞或 DB 被篡改，拒绝并记录
		log.Printf("refresh: cipher mismatch for hash %s", h[:12])
		return "", "", ErrRefreshInvalid
	}
	if err = model.Pool.QueryRow(ctx,
		`SELECT user_id FROM refresh_tokens WHERE token_hash=$1`, h).Scan(&uid); err != nil || uid == "" {
		return "", "", ErrRefreshInvalid
	}
	newRaw, err = IssueRefreshToken(ctx, uid)
	if err != nil {
		return "", "", err
	}
	return newRaw, uid, nil
}

// RevokeUserRefreshTokens 全量吊销某用户未过期的 refresh token
// （禁用账号、管理员改密、自助改密时调用 → 该用户所有设备被踢出续期）。
func RevokeUserRefreshTokens(ctx context.Context, uid string) {
	if model.Pool == nil || uid == "" {
		return
	}
	_, _ = model.Pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, uid)
}

// RevokeSingle 吊销单个 refresh token（登出）。
func RevokeSingle(ctx context.Context, raw string) {
	if model.Pool == nil || raw == "" {
		return
	}
	_, _ = model.Pool.Exec(ctx,
		`UPDATE refresh_tokens SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, hashToken(raw))
}
