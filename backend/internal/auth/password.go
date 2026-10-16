package auth

import (
	"errors"
	"unicode"
)

// ValidatePasswordStrength 密码策略基线（S1.3 / KI-005）：
// 至少 8 位、同时包含字母与数字、不含空格。
//
// 与前端 zod schema 对齐（login.tsx 注册/改密表单），规则变更须双侧同步。
func ValidatePasswordStrength(pw string) error {
	if len(pw) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	var hasLetter, hasDigit bool
	for _, r := range pw {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		case r == ' ':
			return errors.New("password must not contain spaces")
		}
	}
	if !hasLetter || !hasDigit {
		return errors.New("password must contain both letters and digits")
	}
	return nil
}
