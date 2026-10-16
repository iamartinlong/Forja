package logging

// S4 #4a：结构化日志基座（v1.1-sprint W7-W8）。
//
//   - 全进程统一 slog JSON handler（stdout），常驻 service 字段区分 api/worker；
//   - RequestLogger 替代 fiber 文本 logger：每请求一行，含 request_id(=trace_id)/
//     user/status/latency——与 respond.Envelope.trace_id 同源，日志↔响应可互查；
//   - AsynqLogger 把 asynq 内部日志桥接到 slog（worker 队列事件进同一数据源）。
//
// 约定：字段名 snake_case；错误用 err 字段；业务实体 id 用 <entity>_id 后缀，
// Grafana/Loki 查询按字段而非正则。

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"forja/backend/internal/api/respond"

	"github.com/gofiber/fiber/v2"
)

// Setup 初始化进程级默认 slog（JSON/stdout/info），返回带 service 字段的实例。
func Setup(service string) *slog.Logger {
	l := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})).
		With("service", service)
	slog.SetDefault(l)
	return l
}

// RequestLogger fiber 请求日志：4xx→Warn，5xx→Error，其余 Info。
// 必须注册在 requestid 之后（取 Locals requestid）、JWT 之后（取 username）。
func RequestLogger() fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()

		// respond.* 均为"先写响应再返回 ErrResponded 哨兵"以中断处理链——
		// 哨兵对请求日志是噪音，剥离；同时哨兵路径 status 已是真实值。
		if errors.Is(err, respond.ErrResponded) {
			err = nil
		}
		status := c.Response().StatusCode()
		// 未匹配路由等裸 error 经 fiber ErrorHandler 写响应发生在本中间件之后，
		// 此时 Response 尚未落码——从 *fiber.Error 兜底取真实状态
		if err != nil {
			var fe *fiber.Error
			if errors.As(err, &fe) && status == fiber.StatusOK {
				status = fe.Code
			}
		}

		rid, _ := c.Locals("requestid").(string)
		user, _ := c.Locals("username").(string)
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		}
		attrs := []any{
			"trace_id", rid,
			"request_id", rid,
			"method", c.Method(),
			"path", c.Path(),
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", c.IP(),
		}
		if user != "" {
			attrs = append(attrs, "user", user)
		}
		if err != nil {
			attrs = append(attrs, "err", err.Error())
		}
		slog.Log(c.Context(), level, "http_request", attrs...)
		return err
	}
}

// AsynqLogger asynq.Config.Logger 适配器（asynq.Logger：Debug/Info/Warn/Error/Errorf）。
type AsynqLogger struct{}

func (AsynqLogger) Debug(args ...any) { slog.Debug(argsString(args)) }
func (AsynqLogger) Info(args ...any)  { slog.Info(argsString(args)) }
func (AsynqLogger) Warn(args ...any)  { slog.Warn(argsString(args)) }
func (AsynqLogger) Error(args ...any) { slog.Error(argsString(args)) }

// Fatal asynq 仅在不可恢复处调用；slog 无 Fatal——记 Error 后由调用方自行退出
func (AsynqLogger) Fatal(args ...any) { slog.Error("fatal: " + argsString(args)) }

// argsString asynq 内部日志为 printf 风格首参格式串，原样拼接保语义
func argsString(args []any) string {
	return strings.TrimSuffix(fmt.Sprintln(args...), "\n")
}
