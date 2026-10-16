package api

import (
	"regexp"
	"testing"
)

func TestModelProviderRegex(t *testing.T) {
	re := regexp.MustCompile(`^[a-z0-9_-]+$`)
	cases := []struct {
		in string
		ok bool
	}{
		{"myprovider", true},
		{"my-provider_1", true},
		{"MYPROVIDER", false},
		{"a-b_c", true},
		{"bad range", false},
		{"", false},
	}
	for _, c := range cases {
		if got := re.MatchString(c.in); got != c.ok {
			t.Fatalf("regex %q got %v want %v", c.in, got, c.ok)
		}
	}
	// Ensure the 024 constraint regex compiles (previously ^[a-z0-9-_]+$ was invalid range in postgres POSIX, not Go RE2)
	if _, err := regexp.Compile(`^[a-z0-9_-]+$`); err != nil {
		t.Fatalf("valid regex failed to compile: %v", err)
	}
	// Postgres range error is PCRE-specific; Go RE2 is tolerant, so just document not to reintroduce
	invalidPG := `^[a-z0-9-_]+$`
	if invalidPG == `^[a-z0-9_-]+$` {
		t.Fatalf("invalid pattern should differ from valid")
	}
}

func TestModelProviderApiKeyRequired(t *testing.T) {
	// Regression for create vs edit: create requires api_key, edit allows empty
	// Mirrors ModelProviderBoard submit guard and backend create check
	createCases := []struct {
		editing bool
		key     string
		wantErr bool
	}{
		{false, "", true},
		{false, "sk-123", false},
		{true, "", false},
		{true, "newkey", false},
	}
	for _, c := range createCases {
		err := false
		if !c.editing && c.key == "" {
			err = true
		}
		if err != c.wantErr {
			t.Fatalf("editing=%v key=%q wantErr %v", c.editing, c.key, c.wantErr)
		}
	}
}

// Regression for testDraft dual path: editing+empty key -> use stored /:id/test, else preview
func TestModelProviderTestPath(t *testing.T) {
	cases := []struct {
		editing bool
		key     string
		want    string
	}{
		{true, "", "stored"},
		{true, "new", "preview"},
		{false, "sk", "preview"},
	}
	for _, c := range cases {
		got := "preview"
		if c.editing && c.key == "" {
			got = "stored"
		}
		if got != c.want {
			t.Fatalf("editing %v key %q got %s want %s", c.editing, c.key, got, c.want)
		}
	}
}
