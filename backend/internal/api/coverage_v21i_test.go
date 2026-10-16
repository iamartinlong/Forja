package api

// v2.1 coverage sprint Phase 4c: furnace create/patch/chat branches +
// model provider create/patch/references/test/test-preview (TLS mock for healthy path).

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"forja/backend/internal/model"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func covV21iOpencodeMock(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/session" && r.Method == "POST":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"cov-sess-1"}`)
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/message"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"parts":[{"type":"text","text":"pong"}],"info":{}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCovV21i_FurnaceBranches(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	pid := covV21Project(t, ctx, false)
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM furnaces WHERE project_id=$1 OR name LIKE 'cov21i-%'`, pid)
	})

	// create: bad json / missing base_url / invalid project / capacity bounds / empty name
	code, _ := covDo(t, app, "POST", "/api/v1/furnaces", `{bad-json`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/furnaces", `{"name":"cov21i-a"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/furnaces", `{"name":"cov21i-a","base_url":"http://x","project_id":"bad-id"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/furnaces", `{"name":"cov21i-a","base_url":"http://x","capacity":0}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/furnaces", `{"name":"cov21i-a","base_url":"http://x","capacity":21}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/furnaces", `{"name":"   "}`)
	assert.Equal(t, 400, code)

	// create: per-project auto base_url + auth_ref + drained (skip container ensure)
	code, out := covDo(t, app, "POST", "/api/v1/furnaces",
		`{"name":"cov21i-auto","project_id":"`+pid+`","capacity":3,"status":"drained","auth_ref":"opencode:secret","labels":["env:cov"],"model_profile":{"provider_id":"cov21i-prov","model":"m1"}}`)
	require.Equal(t, 200, code, "%v", out)
	fid, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, fid)

	// create: duplicate name
	code, _ = covDo(t, app, "POST", "/api/v1/furnaces", `{"name":"cov21i-auto","base_url":"http://x"}`)
	assert.Equal(t, 400, code)

	// get: ok + not found
	code, out = covDo(t, app, "GET", "/api/v1/furnaces/"+fid, "")
	require.Equal(t, 200, code, "%v", out)
	code, _ = covDo(t, app, "GET", "/api/v1/furnaces/"+uuid.NewString(), "")
	assert.True(t, code >= 400, "code=%d", code)

	// patch: invalid id / not found / empty body / bad json / empty name / capacity / status / base_url / project_id
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/bad-id", `{"name":"x"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+uuid.NewString(), `{"name":"x"}`)
	assert.True(t, code >= 400, "code=%d", code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{bad-json`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{"name":"  "}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{"capacity":99}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{"status":"bogus"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{"base_url":"  "}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{"project_id":"bad-id"}`)
	assert.Equal(t, 400, code)

	// patch: success (name/capacity/status=drained/auth_ref/base_url/labels/model_profile)
	code, out = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid,
		`{"name":"cov21i-renamed","capacity":5,"status":"drained","auth_ref":"tok","base_url":"http://127.0.0.1:9","labels":["a:b"],"model_profile":{"provider_id":"cov21i-prov","model":"m2"}}`)
	require.Equal(t, 200, code, "%v", out)
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{"project_id":""}`)
	assert.Equal(t, 200, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{"project_id":null}`)
	assert.Equal(t, 200, code)

	// test-chat: mock opencode success
	srv := covV21iOpencodeMock(t)
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{"base_url":"`+srv.URL+`"}`)
	assert.Equal(t, 200, code)
	code, out = covDo(t, app, "POST", "/api/v1/furnaces/"+fid+"/test-chat", `{"prompt":"hi"}`)
	require.Equal(t, 200, code, "%v", out)

	// test-chat: unreachable base_url -> fallback to OPENCODE_URL mock
	t.Setenv("OPENCODE_URL", srv.URL)
	code, _ = covDo(t, app, "PATCH", "/api/v1/furnaces/"+fid, `{"base_url":"http://no-such-host-cov21i.invalid:1"}`)
	assert.Equal(t, 200, code)
	code, out = covDo(t, app, "POST", "/api/v1/furnaces/"+fid+"/test-chat", `{"prompt":"hi","model":"m3"}`)
	require.Equal(t, 200, code, "%v", out)

	// test-chat: invalid id / not found
	code, _ = covDo(t, app, "POST", "/api/v1/furnaces/bad-id/test-chat", `{}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/furnaces/"+uuid.NewString()+"/test-chat", `{}`)
	assert.True(t, code >= 400, "code=%d", code)

	// test-preview-chat: missing base_url / success with auth_ref user:pass / token + long prompt
	code, _ = covDo(t, app, "POST", "/api/v1/furnaces/test-preview-chat", `{"provider_id":"cov21i-prov"}`)
	assert.Equal(t, 400, code)
	code, out = covDo(t, app, "POST", "/api/v1/furnaces/test-preview-chat", `{"base_url":"`+srv.URL+`","auth_ref":"u:p","prompt":"p"}`)
	require.Equal(t, 200, code, "%v", out)
	code, out = covDo(t, app, "POST", "/api/v1/furnaces/test-preview-chat", `{"base_url":"`+srv.URL+`","auth_ref":"token","prompt":"`+strings.Repeat("x", 600)+`"}`)
	require.Equal(t, 200, code, "%v", out)

	// delete created furnace (drained static) -> no 5xx
	code, _ = covDo(t, app, "DELETE", "/api/v1/furnaces/"+fid, "")
	assert.True(t, code < 500, "code=%d", code)
}

func TestCovV21i_ModelProviderBranches(t *testing.T) {
	app := covV21App(t)
	if model.Pool == nil {
		t.Skip("DB not available")
	}
	ctx := context.Background()
	providerID := "cov21i-" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = model.Pool.Exec(ctx, `DELETE FROM model_providers WHERE provider_id LIKE 'cov21i-%'`)
	})

	// create: bad json / missing provider_id / invalid auth_mode / Bearer without key / invalid base_url
	code, _ := covDo(t, app, "POST", "/api/v1/model-providers", `{bad-json`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/model-providers", `{"base_url":"https://a.b.com"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/model-providers", `{"provider_id":"`+providerID+`","base_url":"https://a.b.com","auth_mode":"Bogus"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/model-providers", `{"provider_id":"`+providerID+`","base_url":"https://a.b.com"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/model-providers", `{"provider_id":"`+providerID+`","base_url":"http://insecure","api_key":"k"}`)
	assert.Equal(t, 400, code)

	// create: Header mode + headers + models
	code, out := covDo(t, app, "POST", "/api/v1/model-providers",
		`{"provider_id":"`+providerID+`","display_name":"Cov21i","base_url":"https://api.cov21i.example.com/v1","auth_mode":"Header","models":[{"model_id":"m1"},{"model_id":"m2","enabled":false}],"headers":[{"name":"X-K","value":"v"}]}`)
	require.Equal(t, 200, code, "%v", out)
	provUUID, _ := covData(t, out)["id"].(string)
	require.NotEmpty(t, provUUID)

	// create: duplicate provider_id
	code, _ = covDo(t, app, "POST", "/api/v1/model-providers", `{"provider_id":"`+providerID+`","base_url":"https://api.cov21i.example.com","auth_mode":"None"}`)
	assert.Equal(t, 400, code)

	// get ok
	code, _ = covDo(t, app, "GET", "/api/v1/model-providers/"+provUUID, "")
	assert.Equal(t, 200, code)

	// references: invalid id / not found / with referencing furnace
	code, _ = covDo(t, app, "GET", "/api/v1/model-providers/bad-id/references", "")
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "GET", "/api/v1/model-providers/"+uuid.NewString()+"/references", "")
	assert.True(t, code >= 400, "code=%d", code)
	_, err := model.Pool.Exec(ctx,
		`INSERT INTO furnaces (id, name, base_url, capacity, status, model_profile) VALUES (gen_random_uuid(),'cov21i-ref-furnace','http://x',1,'drained',$1::jsonb)`,
		`{"provider_id":"`+providerID+`","model":"m1"}`)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = model.Pool.Exec(ctx, `DELETE FROM furnaces WHERE name='cov21i-ref-furnace'`) })
	code, out = covDo(t, app, "GET", "/api/v1/model-providers/"+provUUID+"/references", "")
	require.Equal(t, 200, code, "%v", out)
	furns, _ := covData(t, out)["furnaces"].([]any)
	assert.GreaterOrEqual(t, len(furns), 1)

	// patch: empty body / invalid id / empty provider_id / invalid base_url / invalid auth_mode
	code, _ = covDo(t, app, "PATCH", "/api/v1/model-providers/"+provUUID, `{}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/model-providers/bad-id", `{"display_name":"x"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/model-providers/"+provUUID, `{"provider_id":"  "}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/model-providers/"+provUUID, `{"base_url":"http://x"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "PATCH", "/api/v1/model-providers/"+provUUID, `{"auth_mode":"Bogus"}`)
	assert.Equal(t, 400, code)
	// patch: removing referenced model -> 400 REFERENCED
	code, _ = covDo(t, app, "PATCH", "/api/v1/model-providers/"+provUUID, `{"models":[{"model_id":"m9"}]}`)
	assert.Equal(t, 400, code)
	// patch: success keeping models
	code, out = covDo(t, app, "PATCH", "/api/v1/model-providers/"+provUUID,
		`{"display_name":"Cov21i Renamed","base_url":"https://api.cov21i.example.com/v2","api_key":"k2","auth_mode":"Bearer","models":[{"model_id":"m1"},{"model_id":"m2"}],"headers":[{"name":"X-A","value":"1"}]}`)
	require.Equal(t, 200, code, "%v", out)

	// test: unreachable -> unhealthy
	code, out = covDo(t, app, "POST", "/api/v1/model-providers/"+provUUID+"/test", "")
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, "unhealthy", covData(t, out)["status"])

	// test: TLS mock -> healthy（合法域名 + DialContext 重定向到本地 TLS 端口）
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[]}`)
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(ts.Close)
	port := ts.Listener.Addr().(*net.TCPAddr).Port
	old := http.DefaultTransport
	http.DefaultTransport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	t.Cleanup(func() { http.DefaultTransport = old })
	const covBaseURL = "https://api.cov21i.example.com"
	_, err = model.Pool.Exec(ctx, `UPDATE model_providers SET base_url=$2, api_key_ref=NULL WHERE id=$1`, provUUID, covBaseURL+"/v1")
	require.NoError(t, err)
	code, out = covDo(t, app, "POST", "/api/v1/model-providers/"+provUUID+"/test", "")
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, "healthy", covData(t, out)["status"])

	// test: not found
	code, _ = covDo(t, app, "POST", "/api/v1/model-providers/"+uuid.NewString()+"/test", "")
	assert.True(t, code >= 400, "code=%d", code)

	// test-preview: missing base_url / non-https / Bearer without key / Header mode healthy
	code, _ = covDo(t, app, "POST", "/api/v1/model-providers/test-preview", `{}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/model-providers/test-preview", `{"base_url":"http://x","api_key":"k"}`)
	assert.Equal(t, 400, code)
	code, _ = covDo(t, app, "POST", "/api/v1/model-providers/test-preview", `{"base_url":"https://a.b.com"}`)
	assert.Equal(t, 400, code)
	code, out = covDo(t, app, "POST", "/api/v1/model-providers/test-preview",
		`{"base_url":"`+covBaseURL+`","auth_mode":"Header","headers":[{"name":"X-P","value":"1"}]}`)
	require.Equal(t, 200, code, "%v", out)
	assert.Equal(t, "healthy", covData(t, out)["status"])

	// delete: existing -> no 5xx
	code, _ = covDo(t, app, "DELETE", "/api/v1/model-providers/"+provUUID, "")
	assert.True(t, code < 500, "code=%d", code)
}
