package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"forja/backend/internal/api"
	"forja/backend/internal/furnace"
	"forja/backend/internal/gitea"
	"forja/backend/internal/logging"
	"forja/backend/internal/metrics"
	"forja/backend/internal/model"
	"forja/backend/internal/queue"

	"github.com/hibiken/asynq"
)

func main() {
	logging.Setup("worker")
	// 与 server/main.go tryInitDB 同理：迁移加固后冷启动窗变长（pgvector initdb 分钟级），
	// 15s 一次性尝试会误杀；快速失败依赖 compose restart 反复拉起太粗暴，就地重试。
	// 与 server 同理：单次 InitDB 超时可放宽（FORJA_DBINIT_TIMEOUT，默认 60s）
	dbTimeout := 60 * time.Second
	if v := os.Getenv("FORJA_DBINIT_TIMEOUT"); v != "" {
		if d, perr := time.ParseDuration(v); perr == nil && d > 0 {
			dbTimeout = d
		}
	}
	for i := 1; ; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
		err := model.InitDB(ctx)
		cancel()
		if err == nil {
			break
		}
		slog.Warn("worker db init failed", "attempt", i, "err", err.Error())
		time.Sleep(5 * time.Second)
	}
	defer model.CloseDB()
	slog.Info("worker db connected")

	queue.Init()
	_ = furnace.EnsureDefaultOnBoot(context.Background())
	furnace.StartReaper(context.Background())
	api.StartTaskReaper(context.Background())
	furnace.StartHealthProbe(context.Background())
	// S4 #4b：worker 指标 + 队列深度采样（30s）
	metrics.SetService("worker")
	metrics.StartQueueSampler(os.Getenv("REDIS_ADDR"))
	// S4 #3：worker 健康端点（:8081/ready，DB+Redis 真探测），供 compose healthcheck
	api.StartWorkerReadyServer(os.Getenv("WORKER_READY_ADDR"))
	srv := queue.NewServer(logging.AsynqLogger{})
	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeForge, func(ctx context.Context, t *asynq.Task) error {
		var p queue.ForgePayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return err
		}
		start := time.Now()
		slog.Info("forge start", "task_id", p.TaskID)
		api.RunForge(context.Background(), p.TaskID)
		slog.Info("forge done", "task_id", p.TaskID, "duration_ms", time.Since(start).Milliseconds())
		return nil
	})
	// S6 定时部署：到点代为执行 deploy dispatch（审计 actor=scheduler:<原操作者>）
	mux.HandleFunc(queue.TypeDeployScheduled, func(ctx context.Context, t *asynq.Task) error {
		var p queue.DeployScheduledPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return err
		}
		if len(p.SHA) < 8 { // 防御畸形 payload：slice 越界会让 asynq 陷入无谓重试
			return fmt.Errorf("deploy scheduled payload: bad sha %q", p.SHA)
		}
		// P0 定时部署按项目隔离：payload 携带 project_id/repo，缺失时回退 SelfRepo 映射
		effectivePID := p.ProjectID
		if effectivePID == "" && p.Repo != "" {
			effectivePID = api.ProjectIDByRepo(p.Repo)
		}
		client := gitea.NewFromEnv()
		if client == nil {
			// 不可恢复（配置缺失，重试无意义）：终止排期行并结束任务
			api.FailScheduledDeployment(context.Background(), effectivePID, "gitea client unavailable")
			return nil
		}
		owner, repo, err := api.SelfRepo(ctx, client)
		if err != nil {
			api.FailScheduledDeployment(ctx, effectivePID, "self repo resolve: "+err.Error())
			return nil
		}
		if effectivePID == "" {
			effectivePID = api.ProjectIDByRepo(owner + "/" + repo)
		}
		slog.Info("scheduled deploy firing", "sha", p.SHA[:8], "actor", p.Actor, "project_id", effectivePID)
		api.MarkScheduledRunning(context.Background(), effectivePID) // §11.3 scheduled -> running (per-project)
		if err := api.ExecuteDeploy(context.Background(), client, owner, repo, p.SHA, api.ProjectDefaultBranch(context.Background(), effectivePID)); err != nil {
			return err // MaxRetry(3) 交给 asynq；校验类错误同样耗尽重试后进 archived
		}
		pid := effectivePID
		if pid == "" {
			pid = api.ProjectIDByRepo(repo)
		}
		api.LogAuditPublic(ctx, pid, "scheduler:"+p.Actor, "deploy.dispatched", "repo", "",
			fmt.Sprintf(`{"sha":%q,"tag":"master-%s","trigger":"scheduled"}`, p.SHA, p.SHA[:8]))
		return nil
	})

	// also handle default queue for dag ready parallel: enqueue next ready tasks after each completion
	if err := srv.Run(mux); err != nil {
		slog.Error("asynq server exited", "err", err.Error())
		os.Exit(1)
	}
}
