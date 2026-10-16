package templates

// v2.1 组织级模板库：从 FORJA_TEMPLATES_REPO（默认 forja-admin/templates）拉取
// `templates/<id>/template.yaml` + 文件，校验后合并到内置列表（同名以组织库为准）。
// - 未配置 FORJA_TEMPLATES_REPO 时纯内置（离线/测试/新装默认）
// - 缓存 TTL 5min；刷新失败静默回退旧缓存/内置（仅告警，不阻塞建项）
// - POST /admin/templates/sync 可强制刷新（Refresh）

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"forja/backend/internal/gitea"

	"gopkg.in/yaml.v3"
)

const (
	cacheTTL       = 5 * time.Minute
	refreshTimeout = 10 * time.Second

	maxTemplateFileSize  = 256 * 1024      // 单文件 ≤256KB
	maxTemplateTotalSize = 2 * 1024 * 1024 // 总量 ≤2MB
	maxTemplateFiles     = 200
)

// manifestFile 组织库模板 manifest（template.yaml）。
type manifestFile struct {
	ID          string         `yaml:"id"`
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Version     string         `yaml:"version"`
	Stack       map[string]any `yaml:"stack"`
	// Options 模板参数声明（v2.2 T2）。
	Options []Option `yaml:"options"`
}

var (
	regMu      sync.RWMutex
	regCache   []*Template
	regCacheAt time.Time
)

// TemplatesRepo 配置的模板库仓（空 = 仅内置）。
func TemplatesRepo() (owner, repo, ref string, ok bool) {
	v := strings.TrimSpace(os.Getenv("FORJA_TEMPLATES_REPO"))
	if v == "" {
		return "", "", "", false
	}
	parts := strings.SplitN(v, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		slog.Warn("invalid FORJA_TEMPLATES_REPO (want owner/repo)", "value", v)
		return "", "", "", false
	}
	ref = strings.TrimSpace(os.Getenv("FORJA_TEMPLATES_REF"))
	if ref == "" {
		ref = "master"
	}
	return parts[0], parts[1], ref, true
}

// Get 按 id 取模板（组织库缓存优先，内置兜底）。
func Get(id string) (*Template, bool) {
	for _, t := range Snapshot() {
		if t.ID == id {
			return t, true
		}
	}
	return nil, false
}

// List 可用模板（组织库 + 内置，按 ID 排序）。
func List() []*Template { return Snapshot() }

// Snapshot 返回合并后的模板列表（TTL 缓存；过期尝试刷新，失败回退）。
func Snapshot() []*Template {
	regMu.RLock()
	entries, at := regCache, regCacheAt
	regMu.RUnlock()

	if time.Since(at) < cacheTTL {
		if entries != nil {
			return entries
		}
		return builtinList()
	}
	owner, repo, ref, enabled := TemplatesRepo()
	gc := gitea.NewFromEnv()
	if !enabled || gc == nil {
		regMu.Lock()
		regCacheAt = time.Now()
		regMu.Unlock()
		if entries != nil {
			return entries
		}
		return builtinList()
	}
	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()
	out, err := load(ctx, gc, owner, repo, ref)
	regMu.Lock()
	regCacheAt = time.Now()
	if err == nil {
		regCache = out
	}
	regMu.Unlock()
	if err != nil {
		slog.Warn("templates registry refresh failed", "repo", owner+"/"+repo, "err", err.Error())
		if entries != nil {
			return entries
		}
		return builtinList()
	}
	return out
}

// Refresh 强制刷新（admin sync）；失败返回错误且保留旧缓存。
func Refresh(ctx context.Context, gc *gitea.Client) ([]*Template, error) {
	owner, repo, ref, enabled := TemplatesRepo()
	if !enabled {
		return nil, fmt.Errorf("FORJA_TEMPLATES_REPO not configured")
	}
	out, err := load(ctx, gc, owner, repo, ref)
	regMu.Lock()
	regCacheAt = time.Now()
	if err == nil {
		regCache = out
	}
	regMu.Unlock()
	if err != nil {
		return nil, err
	}
	return out, nil
}

// load 拉取组织库并合并内置（同名以组织库为准）。
func load(ctx context.Context, gc *gitea.Client, owner, repo, ref string) ([]*Template, error) {
	dirs, err := gc.ListDir(ctx, owner, repo, "templates", ref)
	if err != nil {
		return nil, err
	}
	if dirs == nil {
		return nil, fmt.Errorf("dir templates/ not found in %s/%s@%s", owner, repo, ref)
	}
	var reg []*Template
	for _, d := range dirs {
		if d.Type != "dir" {
			continue
		}
		t, terr := loadOne(ctx, gc, owner, repo, ref, d.Name)
		if terr != nil {
			return nil, fmt.Errorf("template %s: %w", d.Name, terr)
		}
		reg = append(reg, t)
	}
	merged := builtinList()
	for _, r := range reg {
		replaced := false
		for i, b := range merged {
			if b.ID == r.ID {
				merged[i] = r
				replaced = true
				break
			}
		}
		if !replaced {
			merged = append(merged, r)
		}
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].ID < merged[j].ID })
	return merged, nil
}

// loadOne 读取单个模板目录（递归）并做安全校验。
func loadOne(ctx context.Context, gc *gitea.Client, owner, repo, ref, id string) (*Template, error) {
	base := "templates/" + id
	files := map[string]string{}

	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := gc.ListDir(ctx, owner, repo, dir, ref)
		if err != nil {
			return err
		}
		if entries == nil {
			return fmt.Errorf("dir not found: %s", dir)
		}
		for _, e := range entries {
			switch e.Type {
			case "dir":
				if err := walk(e.Path); err != nil {
					return err
				}
			case "file":
				if e.Size > maxTemplateFileSize {
					return fmt.Errorf("file too large: %s (%d bytes)", e.Path, e.Size)
				}
				fc, ferr := gc.GetFileContents(ctx, owner, repo, e.Path, ref)
				if ferr != nil {
					return ferr
				}
				if fc == nil {
					continue
				}
				b, derr := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(fc.Content), "\n", ""))
				if derr != nil {
					return fmt.Errorf("decode %s: %w", e.Path, derr)
				}
				rel := strings.TrimPrefix(strings.TrimPrefix(e.Path, base), "/")
				files[rel] = string(b)
			default:
				return fmt.Errorf("unsupported entry type %q: %s", e.Type, e.Path)
			}
		}
		return nil
	}
	if err := walk(base); err != nil {
		return nil, err
	}

	mfRaw, ok := files["template.yaml"]
	if !ok {
		return nil, fmt.Errorf("template.yaml missing")
	}
	var mf manifestFile
	dec := yaml.NewDecoder(strings.NewReader(mfRaw))
	dec.KnownFields(true)
	if err := dec.Decode(&mf); err != nil {
		return nil, fmt.Errorf("template.yaml: %w", err)
	}
	if mf.ID != id {
		return nil, fmt.Errorf("manifest id %q != dir %q", mf.ID, id)
	}
	if strings.TrimSpace(mf.Name) == "" || strings.TrimSpace(mf.Version) == "" {
		return nil, fmt.Errorf("manifest name/version required")
	}
	if err := ValidateOptionsSchema(mf.Options); err != nil {
		return nil, fmt.Errorf("manifest options: %w", err)
	}
	delete(files, "template.yaml") // manifest 不落项目仓

	for _, req := range []string{"project.yaml", "Dockerfile", ".gitea/workflows/build.yml"} {
		if _, ok := files[req]; !ok {
			return nil, fmt.Errorf("required file missing: %s", req)
		}
	}
	if len(files) > maxTemplateFiles {
		return nil, fmt.Errorf("too many files: %d", len(files))
	}
	total := 0
	for p, c := range files {
		if strings.Contains(p, "..") || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) {
			return nil, fmt.Errorf("unsafe path: %s", p)
		}
		total += len(c)
	}
	if total > maxTemplateTotalSize {
		return nil, fmt.Errorf("template too large: %d bytes", total)
	}

	return &Template{
		ID:          mf.ID,
		Name:        mf.Name,
		Description: mf.Description,
		Version:     mf.Version,
		Source:      SourceRegistry,
		Stack:       mf.Stack,
		Options:     mf.Options,
		raw:         files,
	}, nil
}

// ResetCacheForTest 测试用：清空组织库缓存。
func ResetCacheForTest() {
	regMu.Lock()
	regCache = nil
	regCacheAt = time.Time{}
	regMu.Unlock()
}
