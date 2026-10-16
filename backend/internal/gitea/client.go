// Package gitea FORJA ↔ Gitea REST API 集成客户端（M-CICD-4，design §5）。
//
// 能力：建仓（幂等）/ 脚手架首提 / workflow dispatch / webhook 注册。
// 配置：GITEA_URL + GITEA_TOKEN（env）；未配置时 NewFromEnv 返回 nil，
// 调用方跳过集成而不阻塞项目创建主流程。
package gitea

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Client struct {
	BaseURL string // 如 http://gitea:3000（不带尾斜杠）
	Token   string
	HTTP    *http.Client

	ownerOnceDone bool
	owner         string
}

// NewFromEnv 从环境变量构建客户端；GITEA_URL 或 GITEA_TOKEN 缺失时返回 nil。
func NewFromEnv() *Client {
	url := trimSlash(getenv("GITEA_URL"))
	token := getenv("GITEA_TOKEN")
	if url == "" || token == "" {
		return nil
	}
	return &Client{BaseURL: url, Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// do 执行 JSON API 请求。out 非 nil 时解析响应体。
func (c *Client) do(ctx context.Context, method, path string, in, out any) (int, []byte, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "token "+c.Token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if out != nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, out)
	}
	return resp.StatusCode, raw, nil
}

// owner 当前认证用户/组织名（懒解析缓存）。Org env 优先。
func (c *Client) resolveOwner(ctx context.Context) (string, error) {
	if org := getenv("GITEA_ORG"); org != "" {
		return org, nil
	}
	if c.ownerOnceDone && c.owner != "" {
		return c.owner, nil
	}
	var u struct{ Login string }
	if _, _, err := c.do(ctx, http.MethodGet, "/api/v1/user", nil, &u); err != nil {
		return "", err
	}
	if u.Login == "" {
		return "", fmt.Errorf("gitea: empty login for token")
	}
	c.owner, c.ownerOnceDone = u.Login, true
	return c.owner, nil
}

// CreateRepo 幂等建仓：已存在（409）时直接返回既有地址。
func (c *Client) CreateRepo(ctx context.Context, name, description string) (fullName, htmlURL string, err error) {
	owner, err := c.resolveOwner(ctx)
	if err != nil {
		return "", "", err
	}
	in := map[string]any{
		"name": name, "description": description,
		"private": true, "auto_init": false,
		"default_branch": "master",
	}
	var out struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	}
	code, raw, err := c.do(ctx, http.MethodPost, "/api/v1/user/repos", in, &out)
	if err != nil {
		return "", "", err
	}
	if code == http.StatusCreated && out.FullName != "" {
		return out.FullName, out.HTMLURL, nil
	}
	// 已存在 → 回查既有仓库
	if code == http.StatusConflict || containsAny(raw, "already exist") {
		var ex struct {
			FullName string `json:"full_name"`
			HTMLURL  string `json:"html_url"`
		}
		code2, _, err2 := c.do(ctx, http.MethodGet, "/api/v1/repos/"+owner+"/"+name, nil, &ex)
		if err2 == nil && code2 == http.StatusOK && ex.FullName != "" {
			return ex.FullName, ex.HTMLURL, nil
		}
	}
	return "", "", fmt.Errorf("gitea: create repo %s http %d: %s", name, code, truncate(raw))
}

// ScaffoldFile 脚手架文件条目。
type ScaffoldFile struct{ Path, Content string }

// CommitScaffold 以单次 Changes API 提交全部脚手架文件。
// 对空仓库友好（自动创建初始提交）；branch 建议传 master。
func (c *Client) CommitScaffold(ctx context.Context, owner, repo, branch string, files []ScaffoldFile) (int, error) {
	if len(files) == 0 {
		return 0, nil
	}
	list := make([]map[string]any, 0, len(files))
	for _, f := range files {
		list = append(list, map[string]any{
			"operation": "create",
			"path":      f.Path,
			"content":   base64.StdEncoding.EncodeToString([]byte(f.Content)),
		})
	}
	in := map[string]any{
		"message": fmt.Sprintf("chore(scaffold): initial commit (%d files)", len(files)),
		"branch":  branch,
		"files":   list,
	}
	code, raw, err := c.do(ctx, http.MethodPost, "/api/v1/repos/"+owner+"/"+repo+"/contents", in, nil)
	if err != nil {
		return 0, err
	}
	if code != http.StatusOK && code != http.StatusCreated {
		return 0, fmt.Errorf("gitea: scaffold http %d: %s", code, truncate(raw))
	}
	return len(files), nil
}

// Dispatch 触发 workflow（workflow_dispatch 事件）。
func (c *Client) Dispatch(ctx context.Context, owner, repo, workflow, ref string) error {
	in := map[string]any{"ref": ref}
	code, raw, err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("/api/v1/repos/%s/%s/actions/workflows/%s/dispatches", owner, repo, workflow), in, nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK && code != http.StatusNoContent && code != http.StatusCreated {
		return fmt.Errorf("gitea: dispatch %s http %d: %s", workflow, code, truncate(raw))
	}
	return nil
}

// Owner 公开暴露认证主体解析（org env 优先）；S6 dispatch 代理定位目标仓用。
func (c *Client) Owner(ctx context.Context) (string, error) { return c.resolveOwner(ctx) }

// BranchCommit 取分支当前 HEAD commit sha。
// 用途：S6 构建锚定校验——实测本 Gitea 版本 dispatch API 的 ref 仅接受分支/tag，
// 不接受 commit sha（404 "ref doesn't exist"），故手动构建只允许 master HEAD。
func (c *Client) BranchCommit(ctx context.Context, owner, repo, branch string) (string, error) {
	var out struct {
		Commit struct{ ID string } `json:"commit"`
	}
	code, raw, err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/api/v1/repos/%s/%s/branches/%s", owner, repo, branch), nil, &out)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK || out.Commit.ID == "" {
		return "", fmt.Errorf("gitea: branch %s http %d: %s", branch, code, truncate(raw))
	}
	return out.Commit.ID, nil
}

// SetVariable 写 repo Actions Variable（S6 DEPLOY_TAG 协议）。
// 实测 Gitea variables API：PATCH 不支持（405）；PUT 更新 / POST 创建，
// key 均在路径中、body 仅 {"value":...}。更新优先（PUT），不存在（404）时降级 POST。
func (c *Client) SetVariable(ctx context.Context, owner, repo, key, value string) error {
	in := map[string]string{"value": value}
	base := fmt.Sprintf("/api/v1/repos/%s/%s/actions/variables/%s", owner, repo, key)
	code, raw, err := c.do(ctx, http.MethodPut, base, in, nil)
	if err != nil {
		return err
	}
	if code == http.StatusOK || code == http.StatusNoContent || code == http.StatusCreated {
		return nil
	}
	if code != http.StatusNotFound {
		return fmt.Errorf("gitea: variable %s update http %d: %s", key, code, truncate(raw))
	}
	code, raw, err = c.do(ctx, http.MethodPost, base, in, nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK && code != http.StatusCreated && code != http.StatusNoContent {
		return fmt.Errorf("gitea: variable %s create http %d: %s", key, code, truncate(raw))
	}
	return nil
}

// ActionRun Gitea Actions workflow run 摘要（对账用，observability-design §3.3）
type ActionRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	HeadBranch string `json:"head_branch"`
	HeadSHA    string `json:"head_sha"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	HTMLURL    string `json:"html_url"`
	RunNumber  int64  `json:"run_number"`
}

// ListActionRuns 拉取仓库最近的 Actions runs（webhook 丢失时对账补漏）。
func (c *Client) ListActionRuns(ctx context.Context, ownerRepo string, limit int) ([]ActionRun, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	var out struct {
		TotalCount   int64       `json:"total_count"`
		WorkflowRuns []ActionRun `json:"workflow_runs"`
	}
	code, raw, err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/api/v1/repos/%s/actions/runs?limit=%d", ownerRepo, limit), nil, &out)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("gitea: action runs http %d: %s", code, truncate(raw))
	}
	return out.WorkflowRuns, nil
}

// EnsureWebhook 注册 push/workflow_job 回调（按目标 URL 去重）。
func (c *Client) EnsureWebhook(ctx context.Context, owner, repo, callbackURL, secret string) error {
	type hook struct {
		Type   string         `json:"type"`
		Config map[string]any `json:"config"`
		Events []string       `json:"events"`
		Active bool           `json:"active"`
	}
	_, listRaw, err := c.do(ctx, http.MethodGet, "/api/v1/repos/"+owner+"/"+repo+"/hooks?limit=50", nil, nil)
	if err != nil {
		return err
	}
	if bytes.Contains(listRaw, []byte(callbackURL)) {
		return nil // 已注册
	}
	h := hook{
		Type: "gitea",
		Config: map[string]any{
			"url":          callbackURL,
			"content_type": "json",
			"secret":       secret,
		},
		Events: []string{"push", "workflow_job"},
		Active: true,
	}
	code, raw, err := c.do(ctx, http.MethodPost, "/api/v1/repos/"+owner+"/"+repo+"/hooks", h, nil)
	if err != nil {
		return err
	}
	if code != http.StatusCreated && code != http.StatusOK {
		return fmt.Errorf("gitea: hook create http %d: %s", code, truncate(raw))
	}
	return nil
}

// RepoInfo 仓库元信息（F0.5：default_branch 落库来源）。
type RepoInfo struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Empty         bool   `json:"empty"`
	Private       bool   `json:"private"`
}

// GetRepo 查询仓库；不存在返回 (nil, nil)。
func (c *Client) GetRepo(ctx context.Context, owner, repo string) (*RepoInfo, error) {
	var out RepoInfo
	code, raw, err := c.do(ctx, http.MethodGet, "/api/v1/repos/"+owner+"/"+repo, nil, &out)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, nil
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("gitea: get repo %s/%s http %d: %s", owner, repo, code, truncate(raw))
	}
	return &out, nil
}

// FileContent Contents API 文件内容（base64）。
type FileContent struct {
	Content string `json:"content"` // base64
	SHA     string `json:"sha"`
}

// GetFileContents 读取仓库文件；不存在返回 (nil, nil)。path 为仓内相对路径。
func (c *Client) GetFileContents(ctx context.Context, owner, repo, path, ref string) (*FileContent, error) {
	p := "/api/v1/repos/" + owner + "/" + repo + "/contents/" + path
	if ref != "" {
		p += "?ref=" + url.QueryEscape(ref)
	}
	var out FileContent
	code, raw, err := c.do(ctx, http.MethodGet, p, nil, &out)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, nil
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("gitea: get contents %s http %d: %s", path, code, truncate(raw))
	}
	return &out, nil
}

// DirEntry Contents API 目录项。
type DirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"` // file | dir | symlink | submodule
	Size int64  `json:"size"`
}

// ListDir 列出仓库目录（Contents API）；目录不存在返回 (nil, nil)。
// 注意：目录响应是数组，不能复用 GetFileContents（其 out 为结构体）。
func (c *Client) ListDir(ctx context.Context, owner, repo, path, ref string) ([]DirEntry, error) {
	p := "/api/v1/repos/" + owner + "/" + repo + "/contents"
	if trimmed := strings.Trim(path, "/"); trimmed != "" {
		p += "/" + trimmed
	}
	if ref != "" {
		p += "?ref=" + url.QueryEscape(ref)
	}
	code, raw, err := c.do(ctx, http.MethodGet, p, nil, nil)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, nil
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("gitea: list dir %s http %d: %s", path, code, truncate(raw))
	}
	var out []DirEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("gitea: list dir %s decode: %w", path, err)
	}
	return out, nil
}

// DeleteRepo 删除仓库（F0.5 生命周期：purge 联动；404 视为已删幂等成功）。
func (c *Client) DeleteRepo(ctx context.Context, owner, repo string) error {
	code, raw, err := c.do(ctx, http.MethodDelete, "/api/v1/repos/"+owner+"/"+repo, nil, nil)
	if err != nil {
		return err
	}
	if code == http.StatusNoContent || code == http.StatusOK || code == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("gitea: delete repo %s/%s http %d: %s", owner, repo, code, truncate(raw))
}

// UpsertFile 创建或更新仓内文件（Contents API）。已存在走 PUT（带 sha 乐观并发），
// 否则 POST；返回更新后的文件 sha。sha 过期时上游 409/422，调用方映射 FILE_CONFLICT。
func (c *Client) UpsertFile(ctx context.Context, owner, repo, branch, path, content, message string) (string, error) {
	base := "/api/v1/repos/" + owner + "/" + repo + "/contents/" + path
	existing, err := c.GetFileContents(ctx, owner, repo, path, branch)
	if err != nil {
		return "", err
	}
	in := map[string]any{
		"message": message,
		"branch":  branch,
		"content": base64.StdEncoding.EncodeToString([]byte(content)),
	}
	method := http.MethodPost
	if existing != nil {
		method = http.MethodPut
		in["sha"] = existing.SHA
	}
	var out struct {
		Content struct {
			SHA string `json:"sha"`
		} `json:"content"`
	}
	code, raw, err := c.do(ctx, method, base, in, &out)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK && code != http.StatusCreated {
		return "", fmt.Errorf("gitea: upsert %s http %d: %s", path, code, truncate(raw))
	}
	return out.Content.SHA, nil
}

// SetSecret 写入仓库 Actions Secret（值为明文，Gitea 侧加密存储）。
func (c *Client) SetSecret(ctx context.Context, owner, repo, name, value string) error {
	code, raw, err := c.do(ctx, http.MethodPut,
		"/api/v1/repos/"+owner+"/"+repo+"/actions/secrets/"+name,
		map[string]string{"data": value}, nil)
	if err != nil {
		return err
	}
	if code != http.StatusCreated && code != http.StatusNoContent && code != http.StatusOK {
		return fmt.Errorf("gitea: set secret %s http %d: %s", name, code, truncate(raw))
	}
	return nil
}

// MigrateRepo 将外部仓库镜像/迁移到本 Gitea 实例（import 模式外网仓入口）。
func (c *Client) MigrateRepo(ctx context.Context, cloneAddr, repoName, description string) (fullName, htmlURL string, err error) {
	owner, err := c.resolveOwner(ctx)
	if err != nil {
		return "", "", err
	}
	var out struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	}
	code, raw, err := c.do(ctx, http.MethodPost, "/api/v1/repos/migrate", map[string]any{
		"clone_addr":  cloneAddr,
		"repo_name":   repoName,
		"repo_owner":  owner,
		"private":     true,
		"mirror":      false,
		"description": description,
	}, &out)
	if err != nil {
		return "", "", err
	}
	if code != http.StatusCreated && code != http.StatusOK {
		return "", "", fmt.Errorf("gitea: migrate %s http %d: %s", cloneAddr, code, truncate(raw))
	}
	return out.FullName, out.HTMLURL, nil
}

// ---------- 小工具 ----------

func getenv(k string) string { return os.Getenv(k) }

func containsAny(b []byte, subs ...string) bool {
	for _, s := range subs {
		if bytes.Contains(b, []byte(s)) {
			return true
		}
	}
	return false
}

func truncate(b []byte) string {
	s := string(b)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
