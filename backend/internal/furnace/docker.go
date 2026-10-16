package furnace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func dockerHost() string {
	h := os.Getenv("DOCKER_HOST")
	if h == "" {
		h = "http://socket-proxy:2375"
	}
	return strings.TrimSuffix(h, "/")
}

// Ensure 幂等创建并启动容器：inspect 存在且镜像一致则 start，否则 rm 后重建；返回 container ID
func Ensure(ctx context.Context, name, image string, env []string) (string, error) {
	host := dockerHost()
	// inspect
	if id, img, ok := inspect(ctx, host, name); ok {
		if img == image {
			_ = start(ctx, host, id)
			return id, nil
		}
		_ = remove(ctx, host, name, true)
	}
	// create
	id, err := create(ctx, host, name, image, env)
	if err != nil {
		// 409 已存在复用
		if strings.Contains(err.Error(), "409") {
			if iid, _, ok := inspect(ctx, host, name); ok {
				_ = start(ctx, host, iid)
				return iid, nil
			}
		}
		return "", err
	}
	_ = start(ctx, host, id)
	return id, nil
}

func Start(ctx context.Context, name string) error { return start(ctx, dockerHost(), name) }
func Stop(ctx context.Context, name string) error  { return stop(ctx, dockerHost(), name) }
func Remove(ctx context.Context, name string) error { return remove(ctx, dockerHost(), name, false) }

func create(ctx context.Context, host, name, image string, env []string) (string, error) {
	if !strings.HasPrefix(image, "ghcr.io/anomalyco/opencode") && !strings.HasPrefix(image, "gitea:3000/forja-admin/forja-furnace") {
		return "", fmt.Errorf("image not in whitelist: %s", image)
	}
	// envAllow 过滤：仅 FORJA_/OPENCODE_/PROVIDER_/ANTHROPIC_
	if len(env) > 0 {
		allowed := []string{"FORJA_", "OPENCODE_", "PROVIDER_", "ANTHROPIC_"}
		filtered := []string{}
		for _, e := range env {
			for _, p := range allowed {
				if strings.HasPrefix(e, p) {
					filtered = append(filtered, e)
					break
				}
			}
		}
		env = filtered
	}
	sanitize := strings.ReplaceAll(name, "/", "-")
	// 尝试从 name 解析 furnace id 后 8 位作为 id 标签（forja-furnace-<id8>）
	labels := map[string]string{
		"forja.furnace":         sanitize,
		"forja.furnace.managed": "true",
	}
	if len(sanitize) > 8 {
		labels["forja.furnace.id"] = sanitize[len(sanitize)-8:]
	}
	body, _ := json.Marshal(map[string]any{
		"Image": image,
		"Env":   env,
		"Labels": labels,
		"HostConfig": map[string]any{
			"NetworkMode":   "superpm_default",
			"RestartPolicy": map[string]string{"Name": "no"},
			"Memory":        int64(512 * 1024 * 1024),
			"NanoCpus":      int64(1e9),
		},
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", host+"/containers/create?name="+url.QueryEscape(sanitize), bytes.NewReader(body))
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
	return out.Id, nil
}

func start(ctx context.Context, host, idOrName string) error {
	req, _ := http.NewRequestWithContext(ctx, "POST", host+"/containers/"+url.PathEscape(idOrName)+"/start", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 304 {
		return nil
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("start %d", resp.StatusCode)
	}
	return nil
}

func stop(ctx context.Context, host, idOrName string) error {
	req, _ := http.NewRequestWithContext(ctx, "POST", host+"/containers/"+url.PathEscape(idOrName)+"/stop?t=10", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 304 || resp.StatusCode == 404 {
		return nil
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("stop %d", resp.StatusCode)
	}
	return nil
}

func remove(ctx context.Context, host, idOrName string, force bool) error {
	u := host + "/containers/" + url.PathEscape(idOrName) + "?v=1"
	if force {
		u += "&force=1"
	}
	req, _ := http.NewRequestWithContext(ctx, "DELETE", u, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("remove %d", resp.StatusCode)
	}
	return nil
}

func ListManaged(ctx context.Context) ([]string, error) {
	host := dockerHost()
	req, _ := http.NewRequestWithContext(ctx, "GET", host+"/containers/json?all=1&filters="+url.QueryEscape(`{"label":["forja.furnace.managed=true"]}`), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list %d", resp.StatusCode)
	}
	var out []struct {
		Id    string   `json:"Id"`
		Names []string `json:"Names"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	var ids []string
	for _, c := range out {
		if len(c.Names) > 0 {
			ids = append(ids, strings.TrimPrefix(c.Names[0], "/"))
		} else {
			ids = append(ids, c.Id[:12])
		}
	}
	return ids, nil
}

func inspect(ctx context.Context, host, name string) (id, image string, ok bool) {
	req, _ := http.NewRequestWithContext(ctx, "GET", host+"/containers/"+url.PathEscape(name)+"/json", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return "", "", false
	}
	defer resp.Body.Close()
	var out struct {
		Id     string `json:"Id"`
		Config struct{ Image string `json:"Image"` } `json:"Config"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Id, out.Config.Image, out.Id != ""
}
