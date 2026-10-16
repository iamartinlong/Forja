// Package ratelimit 敏感端点限流（S1 安全基线，KI-005 登录/改密防爆破）。
//
// 默认阈值：单身份 100 次/分钟，超限返回 429 + Retry-After。
// 后端按 env FORJA_RATE_BACKEND 选择（auto|memory|redis，默认 auto）：
//   - redis：REDIS_ADDR 可达时采用固定窗口计数（多实例共享，HPA 就绪）
//   - memory：进程内滑动窗口（单实例兜底；auto 且 Redis 不可达时降级使用）
//
// Redis 故障期间 fail-open 放行并记录日志：限流器故障不阻断登录主链路，
// 可用性优先于严格性（与 queue 的降级取向一致）。
package ratelimit

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"forja/backend/internal/api/respond"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

// 环境变量名。
const (
	EnvLimit         = "FORJA_RATE_LIMIT"          // 窗口内最大请求数，默认 100
	EnvWindowSeconds = "FORJA_RATE_WINDOW_SECONDS" // 窗口秒数，默认 60
	EnvBackend       = "FORJA_RATE_BACKEND"        // auto | memory | redis，默认 auto
)

// Options 限流配置。
type Options struct {
	Limit  int           // 窗口内最大请求数
	Window time.Duration // 窗口时长
}

func optionsFromEnv() Options {
	o := Options{Limit: 100, Window: 60 * time.Second}
	if n, err := strconv.Atoi(os.Getenv(EnvLimit)); err == nil && n > 0 {
		o.Limit = n
	}
	if n, err := strconv.Atoi(os.Getenv(EnvWindowSeconds)); err == nil && n > 0 {
		o.Window = time.Duration(n) * time.Second
	}
	return o
}

// Limiter 限流判定接口。Allow 报告 key 在当前窗口内是否仍被允许。
type Limiter interface {
	Allow(key string) bool
}

var (
	mu     sync.Mutex
	active Limiter
	win    time.Duration
)

// Use 显式覆盖限流器（测试或嵌入式场景）；生产代码无需调用。
func Use(l Limiter, window time.Duration) {
	mu.Lock()
	defer mu.Unlock()
	active, win = l, window
}

// configured 惰性构建默认限流器。
func configured() (Limiter, time.Duration) {
	mu.Lock()
	defer mu.Unlock()
	if active == nil {
		o := optionsFromEnv()
		win = o.Window
		switch strings.ToLower(os.Getenv(EnvBackend)) {
		case "memory":
			active = newMemoryLimiter(o)
		case "redis":
			active = newRedisLimiter(o)
		default: // auto：Redis 可达用 redis，否则降级 memory
			if addr := os.Getenv("REDIS_ADDR"); addr != "" && redisReachable(addr) {
				active = newRedisLimiter(o)
				log.Printf("ratelimit: redis backend at %s (%d/min)", addr, o.Limit)
			} else {
				active = newMemoryLimiter(o)
				log.Printf("ratelimit: in-memory backend (%d/min), redis unavailable or REDIS_ADDR unset", o.Limit)
			}
		}
	}
	return active, win
}

func redisPassword() string {
	if pwd := os.Getenv("FORJA_REDIS_PASSWORD"); pwd != "" {
		return pwd
	}
	return os.Getenv("REDIS_PASSWORD")
}

func redisReachable(addr string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: addr, Password: redisPassword()})
	defer client.Close()
	return client.Ping(ctx).Err() == nil
}

// ---------- 进程内滑动窗口 ----------

type memoryLimiter struct {
	limit  int
	window time.Duration
	mu     sync.Mutex
	hits   map[string][]time.Time
}

func newMemoryLimiter(o Options) *memoryLimiter {
	m := &memoryLimiter{limit: o.Limit, window: o.Window, hits: map[string][]time.Time{}}
	go m.sweep()
	return m
}

func (m *memoryLimiter) Allow(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-m.window)
	kept := m.hits[key][:0]
	for _, t := range m.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= m.limit {
		m.hits[key] = kept
		return false
	}
	m.hits[key] = append(kept, now)
	return true
}

// sweep 周期清理孤儿键，防止大量一次性 IP 键导致内存长尾增长。
func (m *memoryLimiter) sweep() {
	ticker := time.NewTicker(m.window)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		cutoff := time.Now().Add(-m.window)
		for k, ts := range m.hits {
			kept := ts[:0]
			for _, t := range ts {
				if t.After(cutoff) {
					kept = append(kept, t)
				}
			}
			if len(kept) == 0 {
				delete(m.hits, k)
			} else {
				m.hits[k] = kept
			}
		}
		m.mu.Unlock()
	}
}

// ---------- Redis 固定窗口 ----------

type redisLimiter struct {
	client *redis.Client
	opts   Options
	prefix string
}

func newRedisLimiter(o Options) *redisLimiter {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	return &redisLimiter{
		client: redis.NewClient(&redis.Options{Addr: addr, Password: redisPassword()}),
		opts:   o,
		prefix: "forja:rl",
	}
}

func (r *redisLimiter) Allow(key string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	bucket := time.Now().Unix() / int64(r.opts.Window.Seconds())
	k := fmt.Sprintf("%s:%s:%d", r.prefix, key, bucket)
	n, err := r.client.Incr(ctx, k).Result()
	if err != nil {
		log.Printf("ratelimit: redis incr failed (%v), fail-open", err)
		return true
	}
	if n == 1 {
		r.client.Expire(ctx, k, r.opts.Window)
	}
	return n <= int64(r.opts.Limit)
}

// ---------- Fiber 中间件 ----------

// LoginMiddleware 登录端点限流：按请求体 username 计数（缺省回退 IP）。
// 必须挂在 LoginHandler 之前；Fiber 缓存 body，后续 BodyParser 不受影响。
func LoginMiddleware(c *fiber.Ctx) error {
	var body struct {
		Username string `json:"username"`
	}
	_ = c.BodyParser(&body)
	key := strings.ToLower(strings.TrimSpace(body.Username))
	if key == "" {
		key = "ip:" + c.IP()
	}
	return gate(c, "login:"+key)
}

// AuthedMiddleware 已认证敏感端点（如改密）限流：按 uid 计数（缺省回退 IP）。
// 必须挂在 JWTMiddleware 之后以读取 uid。
func AuthedMiddleware(c *fiber.Ctx) error {
	uid, _ := c.Locals("uid").(string)
	if uid == "" {
		uid = "ip:" + c.IP()
	}
	return gate(c, "authed:"+uid)
}

// IPMiddleware 匿名端点按 IP 限流（如 /auth/refresh），scope 区分业务桶。
func IPMiddleware(scope string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		return gate(c, scope+":ip:"+c.IP())
	}
}

func gate(c *fiber.Ctx, key string) error {
	l, w := configured()
	if !l.Allow(key) {
		c.Set("Retry-After", strconv.Itoa(int(w.Seconds())))
		return respond.TooManyRequests(c, fmt.Sprintf("rate limit exceeded: max %d requests per %s", optionsFromEnv().Limit, w))
	}
	return c.Next()
}
