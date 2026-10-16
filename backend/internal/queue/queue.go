package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/hibiken/asynq"
)

const TypeForge = "forja:forge"

// TypeDeployScheduled S6 定时部署：worker 到点代为 dispatch deploy.yml
const TypeDeployScheduled = "forja:deploy-scheduled"

type ForgePayload struct {
	TaskID string `json:"task_id"`
}

// DeployScheduledPayload 定时部署任务体；Actor 留审计溯源（scheduler:<原操作者>）
// P0 扩展：携带 project_id 与 repo 以实现按项目隔离的在途互斥与回填；缺失时 worker 回退 repo 映射。
type DeployScheduledPayload struct {
	SHA       string `json:"sha"`
	Actor     string `json:"actor"`
	ProjectID string `json:"project_id"`
	Repo      string `json:"repo"`
}

var client *asynq.Client
var inspector *asynq.Inspector

func redisOpt() asynq.RedisClientOpt {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	pwd := os.Getenv("FORJA_REDIS_PASSWORD")
	if pwd == "" {
		pwd = os.Getenv("REDIS_PASSWORD")
	}
	return asynq.RedisClientOpt{Addr: addr, Password: pwd}
}

func Init() {
	opt := redisOpt()
	// test redis reachable; if not, keep nil (fallback to direct)
	client = asynq.NewClient(opt)
	inspector = asynq.NewInspector(opt)
	// quick ping via inspector
	_ = inspector
}

func Client() *asynq.Client { return client }

func EnqueueForge(ctx context.Context, taskID string) error {
	if client == nil {
		Init()
	}
	if client == nil {
		return nil
	}
	payload, _ := json.Marshal(ForgePayload{TaskID: taskID})
	// Timeout 30m（forge 全程 3 轮×agent+validation 可达 15min+，5min 会整杀 handler 触发重试）；
	// MaxRetry 0：forge 自带 3 轮修复内循环，asynq 层重试只会产生并发重复锻造
	// （handler 忽略 ctx，被超时杀掉的 forge 仍后台运行——2026-09-06 一次 PATCH 三次 forge start 实录）
	task := asynq.NewTask(TypeForge, payload, asynq.MaxRetry(0), asynq.Timeout(30*time.Minute))
	info, err := client.Enqueue(task)
	if err != nil {
		slog.Warn("asynq enqueue failed, fallback direct", "task_id", taskID, "err", err.Error())
		return err
	}
	slog.Info("forge enqueued", "task_id", taskID, "msg_id", info.ID)
	return nil
}

// EnqueueDeployScheduled 定时部署入队：delay 后由 worker 执行实际 dispatch。
// asynq 持久化于 Redis 卷，栈重启排期不丢（S6-build-deploy-separation.md §3.2）。
// broker 不可用时返回 error（无 forge 那样的 direct fallback——静默丢排期不可接受）。
// P0 扩展：projectID/repo 可选，缺失时 worker 回退 ProjectIDByRepo。
func EnqueueDeployScheduled(ctx context.Context, sha, actor string, delay time.Duration, projectID ...string) error {
	repo := ""
	pid := ""
	if len(projectID) >= 1 {
		pid = projectID[0]
	}
	if len(projectID) >= 2 {
		repo = projectID[1]
	}
	if client == nil {
		Init()
	}
	if client == nil {
		return fmt.Errorf("asynq client unavailable")
	}
	payload, _ := json.Marshal(DeployScheduledPayload{SHA: sha, Actor: actor, ProjectID: pid, Repo: repo})
	task := asynq.NewTask(TypeDeployScheduled, payload, asynq.MaxRetry(3), asynq.Timeout(5*time.Minute))
	info, err := client.Enqueue(task, asynq.ProcessIn(delay))
	if err != nil {
		slog.Warn("deploy scheduled enqueue failed", "sha", shortSha(sha), "err", err.Error())
		return err
	}
	slog.Info("deploy scheduled", "sha", shortSha(sha), "actor", actor, "project_id", pid, "in", delay.String(), "msg_id", info.ID)
	return nil
}

func shortSha(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func NewServer(logger asynq.Logger) *asynq.Server {
	// 容量 1 炉池化：全局并发由 8→2 防 OOM（单炉 Memory512M，3.8G 红线），forge 单列
	return asynq.NewServer(
		redisOpt(),
		asynq.Config{
			Concurrency: 2,
			Queues: map[string]int{
				"default":  2,
				"critical": 1,
			},
			Logger: logger,
		},
	)
}
