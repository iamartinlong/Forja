package furnace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// spawn via socket-proxy (DinD daemon) — Helm k3d / DinD 均走 http://socket-proxy:2375
func Spawn(ctx context.Context, name, image string, env []string) (string, error) {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "http://socket-proxy:2375"
	}
	body, _ := json.Marshal(map[string]any{
		"Image": image,
		"Env": env,
		"Labels": map[string]string{"forja.furnace": name},
		"HostConfig": map[string]any{"RestartPolicy": map[string]string{"Name": "unless-stopped"}},
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", host+"/containers/create?name="+name, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("create %d", resp.StatusCode)
	}
	var out struct{ Id string `json:"Id"` }
	_ = json.NewDecoder(resp.Body).Decode(&out)
	// start
	req2, _ := http.NewRequestWithContext(ctx, "POST", host+"/containers/"+out.Id+"/start", nil)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		return "", err
	}
	defer resp2.Body.Close()
	if resp2.StatusCode >= 300 {
		return "", fmt.Errorf("start %d", resp2.StatusCode)
	}
	return out.Id, nil
}

// HealthProbe 30s 轮询 /ready — 连续 3 失败标 unhealthy（furnace-pool-design.md:39）
// 对 forja-default 做宿主 fallback：历史 base_url 为 forja-default-furnace:4096 仅 DinD 可达，共享 opencode 在 api 侧为 opencode:4096，宿主为 localhost:4096
func HealthProbe(ctx context.Context, baseURL string) bool {
	// 主探
	if doHealthProbe(ctx, baseURL) {
		return true
	}
	// 仅对默认炉的旧地址做 fallback，避免掩盖真实 per-furnace 容器故障
	if baseURL == "http://forja-default-furnace:4096" || baseURL == "http://forja-default-furnace:4096/" {
		for _, fb := range []string{"http://opencode:4096", "http://localhost:4096", "http://127.0.0.1:4096", "http://host.docker.internal:4096"} {
			if doHealthProbe(ctx, fb) {
				return true
			}
		}
	}
	return false
}

func doHealthProbe(ctx context.Context, baseURL string) bool {
	user := os.Getenv("OPENCODE_USERNAME")
	if user == "" {
		user = "opencode"
	}
	pass := os.Getenv("OPENCODE_PASSWORD")
	if pass == "" {
		pass = os.Getenv("OPENCODE_SERVER_PASSWORD")
	}
	if pass == "" {
		pass = "forja-dev"
	}
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	// 兼容两种健康端点：opencode 为 /global/health（需 Basic Auth），forja-api 为 /ready
	for _, path := range []string{"/ready", "/global/health"} {
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		req, _ := http.NewRequestWithContext(c, "GET", strings.TrimSuffix(baseURL, "/")+path, nil)
		// opencode 需认证，api 的 /ready 无需但带上也无害
		req.Header.Set("Authorization", auth)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 400 {
				return true
			}
		}
	}
	return false
}
