package api

// M-CICD-4b：Gitea Webhook 回流（design §5.3）+ 可观测性 P0 落库（observability-design §3.2）。
//   - 验签：X-Gitea-Signature = hex(HMAC-SHA256(body, GITEA_WEBHOOK_SECRET))
//   - 幂等：X-Gitea-Delivery 去重（webhook_deliveries 表持久化；Pool 不可用时回退进程内 LRU）
//   - 事件：
//       push         → 审计 repo.push + pipeline_runs upsert(event=push)
//       workflow_job → 审计 ci.completed + 定位 run(repo+sha) → upsert job → 聚合 run 结论
//
// Gitea 站点级 Actions 日志与状态以本审计留痕为准；Task 级回写依赖仓库名↔项目 slug
// 约定，随 P1 批次落地。

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/events"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
)

func webhookSecret() string { return os.Getenv("GITEA_WEBHOOK_SECRET") }

const deliveryTTL = time.Hour

var (
	deliveryMu   sync.Mutex
	deliverySeen = map[string]time.Time{}
)

// deliverySeenOnce 进程内去重（仅 DB 不可用时的兜底路径）
func deliverySeenOnce(id string) bool {
	if id == "" {
		return false
	}
	now := time.Now()
	deliveryMu.Lock()
	defer deliveryMu.Unlock()
	for k, t := range deliverySeen { // 惰性过期
		if now.Sub(t) > deliveryTTL {
			delete(deliverySeen, k)
		}
	}
	if _, dup := deliverySeen[id]; dup {
		return true
	}
	deliverySeen[id] = now
	return false
}

// deliverySeenOnceDB 持久化去重：INSERT ON CONFLICT DO NOTHING 影响行数 0 即重复投递。
// DB 不可用时回退进程内 LRU（单实例 dev 场景兜底）。
func deliverySeenOnceDB(ctx context.Context, id, event string) bool {
	if id == "" {
		return false
	}
	if model.Pool == nil {
		return deliverySeenOnce(id)
	}
	tag, err := model.Pool.Exec(ctx,
		`INSERT INTO webhook_deliveries (delivery_id, event) VALUES ($1,$2) ON CONFLICT (delivery_id) DO NOTHING`,
		id, event)
	if err != nil {
		slog.Warn("webhook dedup db error, fallback to memory", "err", err.Error())
		return deliverySeenOnce(id)
	}
	if tag.RowsAffected() == 0 {
		return true
	}
	_, _ = model.Pool.Exec(ctx, `DELETE FROM webhook_deliveries WHERE received_at < now() - interval '7 days'`)
	return false
}

type giteaHookCommit struct {
	Added    []string `json:"added"`
	Removed  []string `json:"removed"`
	Modified []string `json:"modified"`
}

type giteaHookPayload struct {
	Zen     string             `json:"zen"`
	Ref     string             `json:"ref"`
	After   string             `json:"after"`
	Action  string             `json:"action"`
	Commits []giteaHookCommit  `json:"commits"`
	Repo    *struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender *struct {
		Login string `json:"login"`
	} `json:"sender"`
	WorkflowJob *struct {
		Name        string `json:"name"`
		Status      string `json:"status"`
		Conclusion  string `json:"conclusion"`
		HeadSHA     string `json:"head_sha"`
		StartedAt   string `json:"started_at"`
		CompletedAt string `json:"completed_at"`
	} `json:"workflow_job"`
}

// isDocsOnlyPush 全部变更文件命中 docs/** 或 *.md 视为纯文档推送（§11.6）。
// commits 为空（强制推送/新分支等）保守返回 false——宁可多建一行不漏交付。
func isDocsOnlyPush(p *giteaHookPayload) bool {
	if len(p.Commits) == 0 {
		return false
	}
	for _, c := range p.Commits {
		for _, f := range c.Added {
			if !isDocPath(f) {
				return false
			}
		}
		for _, f := range c.Removed {
			if !isDocPath(f) {
				return false
			}
		}
		for _, f := range c.Modified {
			if !isDocPath(f) {
				return false
			}
		}
	}
	return true
}

func isDocPath(path string) bool {
	return strings.HasPrefix(path, "docs/") || strings.HasSuffix(path, ".md")
}

// pushTouchesFile 本次 push 是否触及指定仓内文件（F0.5 缓存刷新触发）
func pushTouchesFile(p *giteaHookPayload, path string) bool {
	for _, c := range p.Commits {
		for _, f := range c.Added {
			if f == path {
				return true
			}
		}
		for _, f := range c.Removed {
			if f == path {
				return true
			}
		}
		for _, f := range c.Modified {
			if f == path {
				return true
			}
		}
	}
	return false
}

func RegisterWebhookRoutes(r fiber.Router) {
	r.Post("/webhooks/gitea", giteaWebhookHandler)
}

func giteaWebhookHandler(c *fiber.Ctx) error {
	secret := webhookSecret()
	if secret == "" {
		return respond.ServiceUnavailable(c)
	}
	body := c.Body()

	sig := c.Get("X-Gitea-Signature")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return respond.Unauthorized(c, "invalid signature")
	}

	deliveryID := c.Get("X-Gitea-Delivery")
	event := c.Get("X-Gitea-Event")
	ctx := c.Context()
	if deliverySeenOnceDB(ctx, deliveryID, event) {
		return respond.OK(c, fiber.Map{"dedup": true})
	}
	reapStaleRuns(ctx) // 僵尸 run 自愈：随每次 webhook 顺带清扫（全局兜底，U-3 按项目隔离后仍保留）

	var p giteaHookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return respond.BadRequest(c, "invalid payload")
	}

	repoName := ""
	actor := "gitea"
	if p.Repo != nil {
		repoName = p.Repo.FullName
	}
	if p.Sender != nil && p.Sender.Login != "" {
		actor = p.Sender.Login
	}

	detail := func(extra string) string {
		var sb strings.Builder
		sb.WriteString(`{"repo":`)
		b, _ := json.Marshal(repoName)
		sb.Write(b)
		sb.WriteString(`,"event":`)
		b, _ = json.Marshal(event)
		sb.Write(b)
		if extra != "" {
			sb.WriteString("," + extra)
		}
		sb.WriteString("}")
		return sb.String()
	}

	switch event {
	case "ping":
		// 连通性测试：仅确认签名，不落业务审计
	case "push":
		ref := strings.TrimPrefix(p.Ref, "refs/heads/")
		slog.Info("webhook push", "repo", repoName, "branch", ref, "sha", p.After, "actor", actor)
		// U-3 扩展：按项目维度清扫僵尸 RUNNING（跨项目不互清）
		if pidPre := projectIDByRepo(repoName); pidPre != "" {
			reapStaleRuns(ctx, pidPre)
		}
		// S6 §11.6：docs-only 提交（全部命中 docs/** 或 *.md）不建 PENDING 行——
		// 纯文档推送没有可交付物，堆积待构建徽章纯属噪音。审计照发。
		// 2026-09-17：标签推送（release tag）同样不建行——tag 不触发构建，且其行会因
		// created_at 更晚抢走 workflow_job 回流（run368/T4 实录：manual 行滞留 RUNNING）。
		isTagRef := strings.HasPrefix(p.Ref, "refs/tags/")
		if !isDocsOnlyPush(&p) && !isTagRef {
			recordPushRun(ctx, repoName, p.After, ref, actor)
		}
		pid := projectIDByRepo(repoName)
		if pid != "" {
			// F0.5：仓库内规范/契约变更 → 异步刷新缓存（真相在仓；不阻塞 webhook 响应）
			if pushTouchesFile(&p, "AGENTS.md") {
				go refreshProjectAgents(context.Background(), pid)
			}
			if pushTouchesFile(&p, "project.yaml") {
				go refreshProjectManifest(context.Background(), pid)
			}
			// P1 SSE：项目频道脏通知（徽章/PipelineBoard 即时刷新）
			events.Default.Publish("project:"+pid, "repo.push",
				map[string]any{"repo": repoName, "branch": ref, "sha": p.After, "actor": actor})
		}
		logAudit(ctx, pid, actor, "repo.push", "repo", "", detail(`"branch":`+quoteJSON(ref)))
	case "workflow_job":
		if p.WorkflowJob == nil {
			break
		}
		switch p.WorkflowJob.Status {
		case "completed":
			j := p.WorkflowJob
			conclusion := j.Conclusion
			slog.Info("webhook workflow_job", "repo", repoName, "job", j.Name,
				"conclusion", conclusion, "sha", j.HeadSHA)
			// U-3 按项目维度清扫
			if pidPre := pipelineProjectID(ctx, repoName, j.HeadSHA); pidPre != "" {
				reapStaleRuns(ctx, pidPre)
			}
			extra := `"job":` + quoteJSON(j.Name) +
				`,"conclusion":` + quoteJSON(conclusion)
			if j.Name == "deploy" {
				// S6 审计修正：deploy.yml dispatch ref=master，回流 head_sha 是 master HEAD
				// 而非 DEPLOY_TAG 目标 commit——落库会给 HEAD 行加 deploy job 并聚合出
				// 假 SUCCESS（未构建的 commit 被 ensureBuilt 误放行）。当前线上版本以
				// deployments 表（§11）+ audit 为准，此处回填实体状态并留审计。
				// P0-1/P0-2 修复：按 project_id 维度隔离回填，优先取 deployments running 行的 project_id，fallback repo 映射
				pid := resolveDeployProjectID(ctx, repoName)
				CompleteRunningDeployment(ctx, pid, conclusion)
				logAudit(ctx, pid, actor, "ci.completed", "repo", "", detail(extra))
				break
			}
			recordJobCompletion(ctx, repoName, j.HeadSHA, j.Name, conclusion, j.StartedAt, j.CompletedAt)
			// M1.2：用户项目构建成功 → 制品台账（作业名 build，模板脚手架约定）
			if conclusion == "success" && j.Name == "build" {
				registerArtifact(ctx, repoName, j.HeadSHA, j.Name)
			}
			pid := pipelineProjectID(ctx, repoName, j.HeadSHA)
			if pid != "" {
				events.Default.Publish("project:"+pid, "ci.completed",
					map[string]any{"repo": repoName, "job": j.Name, "conclusion": conclusion, "sha": j.HeadSHA})
				// P1 G4 回写：CI 结论留痕 RELEASE_PENDING 任务
				linkCIToTask(ctx, repoName, j.HeadSHA, j.Name, conclusion)
				// U-4：完成事件同步触发 progress 通道，PipelineBoard 详情立即刷新
				events.Default.Publish("project:"+pid, "ci.progress",
					map[string]any{"repo": repoName, "job": j.Name, "conclusion": conclusion, "sha": j.HeadSHA})
			}
			logAudit(ctx, pid, actor, "ci.completed", "repo", "", detail(extra))
		case "queued", "in_progress", "waiting":
			j := p.WorkflowJob
			if j.Name == "deploy" {
				break // 同 completed 规则：deploy 不入 pipeline_jobs
			}
			// U-3 按项目维度清扫
			if pidPre := pipelineProjectID(ctx, repoName, j.HeadSHA); pidPre != "" {
				reapStaleRuns(ctx, pidPre)
			}
			slog.Info("webhook workflow_job progress", "repo", repoName, "job", j.Name,
				"status", j.Status, "sha", j.HeadSHA)
			recordJobProgress(ctx, repoName, j.HeadSHA, j.Name, j.Status, j.StartedAt)
			pid := pipelineProjectID(ctx, repoName, j.HeadSHA)
			if pid != "" {
				events.Default.Publish("project:"+pid, "ci.progress",
					map[string]any{"repo": repoName, "job": j.Name, "status": j.Status, "sha": j.HeadSHA})
			}
			extra := `"job":` + quoteJSON(j.Name) + `,"status":` + quoteJSON(j.Status)
			logAudit(ctx, pid, actor, "ci.progress", "repo", "", detail(extra))
		default:
			slog.Info("webhook workflow_job ignored status", "repo", repoName, "job", p.WorkflowJob.Name, "status", p.WorkflowJob.Status)
		}
	default:
		// 其他事件（issue/issues 等）当前不消费
	}
	return respond.OK(c, fiber.Map{"received": true})
}

// pipelineProjectID 查询既有 pipeline_runs 行的归属项目，优先真实落库值，回退 repo 映射
func pipelineProjectID(ctx context.Context, repo, sha string) string {
	if model.Pool != nil && repo != "" && !isZeroSHA(sha) {
		var pid *string
		if err := model.Pool.QueryRow(ctx,
			`SELECT project_id::text FROM pipeline_runs WHERE repo=$1 AND sha=$2 ORDER BY created_at DESC LIMIT 1`, repo, sha).Scan(&pid); err == nil && pid != nil && *pid != "" {
			return *pid
		}
	}
	return projectIDByRepo(repo)
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
