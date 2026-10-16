package api

// SSE 传输辅助（observability P1，design §4）。
// 线格式：统一单事件 data: {"name":"...","data":{...}}\n\n——EventSource.onmessage
// 可捕获全部命名事件（命名 event: 需逐个 addEventListener，前端无法动态枚举）。
// 心跳：15s 注释行；断连靠 Flush 错误检测并退出 StreamWriter。

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"forja/backend/internal/api/respond"
	"forja/backend/internal/auth"
	"forja/backend/internal/events"
	"forja/backend/internal/model"

	"github.com/gofiber/fiber/v2"
)

const sseHeartbeat = 15 * time.Second

func writeSSE(w *bufio.Writer, evt events.Event) error {
	b, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
		return err
	}
	return w.Flush()
}

// sseStream 订阅 channel 并以 SSE 推送；连接建立先推 snapshot。
// 阻塞至客户端断开（Flush 错误）——调用方 return nil 即可，响应体由 writer 接管。
func sseStream(c *fiber.Ctx, channel string, snapshot any) error {
	ch, unsub := events.Default.Subscribe(channel)
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer unsub()
		if err := writeSSE(w, events.Event{Name: "snapshot", Data: mustJSON(snapshot)}); err != nil {
			return
		}
		tick := time.NewTicker(sseHeartbeat)
		defer tick.Stop()
		for {
			select {
			case evt, ok := <-ch:
				if !ok {
					return
				}
				if writeSSE(w, evt) != nil {
					return // 客户端断开
				}
			case <-tick.C:
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return
				}
				if w.Flush() != nil {
					return
				}
			}
		}
	})
	return nil
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// taskSnapshot 流式首帧：前端 Stepper 以此初始化阶段状态（不依赖事件补全历史）
func taskSnapshot(c *fiber.Ctx, taskID string) fiber.Map {
	m := fiber.Map{"task_id": taskID, "status": "PENDING"}
	var status string
	var attempt int
	var sha *string
	if err := model.Pool.QueryRow(context.Background(),
		`SELECT status, forge_attempt, commit_sha FROM tasks WHERE id=$1`, taskID).
		Scan(&status, &attempt, &sha); err == nil {
		m["status"] = status
		m["forge_attempt"] = attempt
		m["commit_sha"] = sha
	}
	return m
}

// RegisterSSERoutes SSE 订阅端点（observability P1）
func RegisterSSERoutes(r fiber.Router) {
	r.Get("/tasks/:id/stream", auth.Require("task.read"), streamTaskLive)
	r.Get("/projects/:id/events", auth.Require("task.read"), projectEvents)
}

// streamTaskLive 真 SSE：替换 P0 前的单次伪流（design §1.2 G2）
func streamTaskLive(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	id := c.Params("id")
	var pid string
	if err := model.Pool.QueryRow(context.Background(),
		`SELECT project_id::text FROM tasks WHERE id=$1`, id).Scan(&pid); err != nil {
		return respond.NotFound(c, "task")
	}
	if err := AuthorizeProject(c, pid, "task.read"); err != nil {
		return err
	}
	return sseStream(c, "task:"+id, taskSnapshot(c, id))
}

// projectEvents 项目频道：徽章/PipelineBoard 的 ci.*、repo.push、任务状态脏通知
func projectEvents(c *fiber.Ctx) error {
	if model.Pool == nil {
		return respond.ServiceUnavailable(c)
	}
	pid := c.Params("id")
	if err := AuthorizeProject(c, pid, "task.read"); err != nil {
		return err
	}
	return sseStream(c, "project:"+pid, fiber.Map{"channel": "project:" + pid})
}
