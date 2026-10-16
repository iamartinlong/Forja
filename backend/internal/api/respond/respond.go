// Package respond 统一 API 响应格式。
//
// 成功: {"code":0,"message":"ok","data":{...},"trace_id":"..."}
// 失败: {"code":40001,"message":"参数错误","error":{"code":"INVALID_PARAM","detail":"title required"},"trace_id":"..."}
//
// 规范文档: docs/architecture/api-contract.md
package respond

import (
	"errors"
	"log/slog"
	"os"
	"strconv"

	"github.com/gofiber/fiber/v2"
)

// ErrResponded 哨兵错误：响应已写出。
//
// 所有 respond.* 辅助函数在写出响应后返回它，使调用方 `return respond.XXX(...)`
// 能真正中断处理函数（Fiber 的 c.JSON 成功时返回 nil，直接透传会导致守卫失效、
// 处理函数继续执行——历史缺陷）。HandleError 捕获该哨兵后不再重复写响应。
var ErrResponded = errors.New("response already written")

// 业务码目录：0 成功；4xxxx 客户端错误；5xxxx 服务端错误。
const (
	CodeOK = 0

	CodeInvalidParam = 40001 // 参数校验失败
	CodeNotFound     = 40002 // 资源不存在
	CodeConflict     = 40003 // 资源冲突（唯一约束等）
	CodeForbidden    = 40004 // 权限不足
	CodeUnauthorized = 40005 // 未认证 / 凭证无效
	CodeBusinessRule = 40006 // 业务规则拒绝（依赖成环、宪法违规、批量超限等）
	CodeUnsupported  = 40007 // 媒体类型 / 内容不支持
	CodeRateLimited  = 40008 // 触发限流（429）

	CodeInternal           = 50001 // 内部错误
	CodeDBError            = 50002 // 数据库错误
	CodeUpstreamError      = 50003 // 上游依赖错误（Agent/向量/估算等）
	CodeServiceUnavailable = 50004 // 服务不可用（DB 未就绪等）
)

// ErrDetail 错误响应中的 error 字段。
type ErrDetail struct {
	Code   string `json:"code"`             // 机器可读错误码，如 INVALID_PARAM
	Detail string `json:"detail,omitempty"` // 人类可读详情
	Field  string `json:"field,omitempty"`  // 出错字段（参数类错误）
}

// Envelope 统一响应包装。
type Envelope struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	Data    any        `json:"data,omitempty"`
	Error   *ErrDetail `json:"error,omitempty"`
	TraceID string     `json:"trace_id,omitempty"`
}

// PageMeta 列表分页元数据，位于 data 内与 items 并列。
type PageMeta struct {
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

// ListData 分页列表 data 结构。
type ListData struct {
	Items any `json:"items"`
	PageMeta
}

// traceID 从 requestid 中间件注入的 Locals 取追踪 ID。
func traceID(c *fiber.Ctx) string {
	if v, ok := c.Locals("requestid").(string); ok {
		return v
	}
	return ""
}

// OK 成功响应，HTTP 200。
func OK(c *fiber.Ctx, data any) error {
	return wrote(c.JSON(Envelope{Code: CodeOK, Message: "ok", Data: data, TraceID: traceID(c)}))
}

// List 分页列表成功响应，HTTP 200。
func List(c *fiber.Ctx, items any, page PageMeta) error {
	return wrote(c.JSON(Envelope{Code: CodeOK, Message: "ok", Data: ListData{Items: items, PageMeta: page}, TraceID: traceID(c)}))
}

// Fail 失败响应。status 为 HTTP 状态码（客户端 4xx / 服务端 5xx）。
func Fail(c *fiber.Ctx, status, bizCode int, message, errCode, detail string) error {
	return wrote(c.Status(status).JSON(Envelope{
		Code:    bizCode,
		Message: message,
		Error:   &ErrDetail{Code: errCode, Detail: detail},
		TraceID: traceID(c),
	}))
}

// BadRequest 参数错误，HTTP 400 / 40001。
func BadRequest(c *fiber.Ctx, detail string) error {
	return Fail(c, fiber.StatusBadRequest, CodeInvalidParam, "invalid request", "INVALID_PARAM", detail)
}

// InvalidParam 指定字段的参数错误，HTTP 400 / 40001。
func InvalidParam(c *fiber.Ctx, field, detail string) error {
	return wrote(c.Status(fiber.StatusBadRequest).JSON(Envelope{
		Code:    CodeInvalidParam,
		Message: "invalid request",
		Error:   &ErrDetail{Code: "INVALID_PARAM", Detail: detail, Field: field},
		TraceID: traceID(c),
	}))
}

// BusinessRule 业务规则拒绝，HTTP 400 / 40006。errCode 如 CYCLE、CONSTITUTION_VIOLATION。
func BusinessRule(c *fiber.Ctx, errCode, detail string) error {
	return Fail(c, fiber.StatusBadRequest, CodeBusinessRule, "business rule rejected", errCode, detail)
}

// NotFound 资源不存在，HTTP 404 / 40002。
func NotFound(c *fiber.Ctx, resource string) error {
	return Fail(c, fiber.StatusNotFound, CodeNotFound, "not found", "NOT_FOUND", resource+" not found")
}

// Conflict 资源冲突，HTTP 409 / 40003。
func Conflict(c *fiber.Ctx, detail string) error {
	return Fail(c, fiber.StatusConflict, CodeConflict, "conflict", "CONFLICT", detail)
}

// Forbidden 权限不足，HTTP 403 / 40004。
func Forbidden(c *fiber.Ctx, detail string) error {
	return Fail(c, fiber.StatusForbidden, CodeForbidden, "forbidden", "FORBIDDEN", detail)
}

// Unauthorized 未认证，HTTP 401 / 40005。
func Unauthorized(c *fiber.Ctx, detail string) error {
	return Fail(c, fiber.StatusUnauthorized, CodeUnauthorized, "unauthorized", "UNAUTHORIZED", detail)
}

// debugErrors 开发排障开关：env DEBUG_ERRORS=1 时 5xx 详情回传客户端（生产禁用）
func debugErrors() bool {
	return os.Getenv("DEBUG_ERRORS") == "1"
}

// DBError 数据库错误，HTTP 500 / 50002。详情仅服务端日志，避免泄露表结构/SQL。
func DBError(c *fiber.Ctx, err error) error {
	slog.Error("db error", "trace_id", traceID(c), "request_id", traceID(c), "err", err.Error())
	detail := "internal database error"
	if debugErrors() {
		detail = err.Error()
	}
	return Fail(c, fiber.StatusInternalServerError, CodeDBError, "database error", "DB_ERROR", detail)
}

// Internal 内部错误，HTTP 500 / 50001。
func Internal(c *fiber.Ctx, err error) error {
	slog.Error("internal error", "trace_id", traceID(c), "request_id", traceID(c), "err", err.Error())
	detail := "internal error"
	if debugErrors() {
		detail = err.Error()
	}
	return Fail(c, fiber.StatusInternalServerError, CodeInternal, "internal error", "INTERNAL", detail)
}

// Upstream 上游依赖错误，HTTP 500 / 50003。errCode 如 PACT_FAILED、ESTIMATE_FAILED。
func Upstream(c *fiber.Ctx, errCode string, err error) error {
	slog.Error("upstream error", "trace_id", traceID(c), "request_id", traceID(c), "code", errCode, "err", err.Error())
	detail := "upstream dependency failed: " + errCode
	if debugErrors() && err != nil {
		detail = err.Error()
	}
	return Fail(c, fiber.StatusInternalServerError, CodeUpstreamError, "upstream error", errCode, detail)
}

// ServiceUnavailable 服务暂不可用（DB 未初始化等），HTTP 503 / 50004。
func ServiceUnavailable(c *fiber.Ctx) error {
	return Fail(c, fiber.StatusServiceUnavailable, CodeServiceUnavailable, "service unavailable", "SERVICE_UNAVAILABLE", "database not initialized")
}

// TooManyRequests 限流拒绝，HTTP 429 / 40008。调用方应先设置 Retry-After 响应头。
func TooManyRequests(c *fiber.Ctx, detail string) error {
	return Fail(c, fiber.StatusTooManyRequests, CodeRateLimited, "too many requests", "RATE_LIMITED", detail)
}

// HandleError 全局兜底错误处理（fiber ErrorHandler 用），
// 将未捕获错误 / 路由不存在等映射为标准 envelope。
func HandleError(c *fiber.Ctx, err error) error {
	// respond.* 已写出响应的哨兵错误：不再重复写，保持原响应
	if errors.Is(err, ErrResponded) {
		return nil
	}
	code := fiber.StatusInternalServerError
	bizCode := CodeInternal
	message := "internal error"
	errCode := "INTERNAL"
	var e *fiber.Error
	if errors.As(err, &e) {
		code = e.Code
		switch {
		case code == fiber.StatusNotFound:
			bizCode, message, errCode = CodeNotFound, "not found", "NOT_FOUND"
		case code == fiber.StatusUnauthorized:
			bizCode, message, errCode = CodeUnauthorized, "unauthorized", "UNAUTHORIZED"
		case code == fiber.StatusForbidden:
			bizCode, message, errCode = CodeForbidden, "forbidden", "FORBIDDEN"
		case code >= 400 && code < 500:
			bizCode, message, errCode = CodeInvalidParam, "invalid request", "INVALID_PARAM"
		default:
			bizCode, message, errCode = CodeInternal, "internal error", "INTERNAL"
		}
	}
	if code >= 500 {
		slog.Error("request error", "trace_id", traceID(c), "request_id", traceID(c), "err", err.Error())
	}
	detail := err.Error()
	if code >= 500 && !debugErrors() {
		detail = "internal server error"
	}
	return c.Status(code).JSON(Envelope{
		Code:    bizCode,
		Message: message,
		Error:   &ErrDetail{Code: errCode, Detail: detail},
		TraceID: traceID(c),
	})
}

// wrote 包装 c.JSON/c.Status 链的返回值：写出成功也返回哨兵错误，
// 保证 `return respond.XXX(...)` 语义上中断处理函数。
func wrote(err error) error {
	if err != nil {
		return err
	}
	return ErrResponded
}

// ParsePage 解析分页参数，默认 page=1、page_size=20，上限 100。
func ParsePage(c *fiber.Ctx) (page, pageSize int) {
	page, _ = strconv.Atoi(c.Query("page", "1"))
	pageSize, _ = strconv.Atoi(c.Query("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}
