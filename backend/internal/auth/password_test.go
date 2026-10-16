package auth

// S1.3 密码策略单元测试。

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidatePasswordStrength(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		ok   bool
	}{
		{"合法_字母数字", "pass1234", true},
		{"合法_含符号", "P@ssw0rd9", true},
		{"过短", "ab1", false},
		{"七位边界", "abcdef1", false},
		{"八位边界", "abcdefg1", true},
		{"纯字母", "abcdefgh", false},
		{"纯数字", "12345678", false},
		{"含空格", "abcd 123", false},
		{"空串", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePasswordStrength(c.pw)
			if c.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}
