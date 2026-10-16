// Package events 进程间事件总线（observability P1，design §4.1）。
//
// 难点：Compose 中 api 与 worker 是两个进程——纯内存 hub 收不到 worker 侧锻造事件。
// 因此默认桥接 Redis Pub/Sub（REDIS_ADDR，栈内已有 go-redis 依赖）：
//   Publish → redis channel → 各进程 pump → 本地 fan-out 给订阅者
// Redis 不可用时降级为纯本地模式（单进程 dev 场景）。
//
// 投递语义：至多一次（慢消费者/断线丢事件可接受——SSE 仅作脏通知，
// 客户端收到即 invalidate query 重拉快照，不依赖事件补全状态）。
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Event SSE 线格式：data: {"name":"status","data":{...}}
type Event struct {
	Name string          `json:"name"`
	Data json.RawMessage `json:"data,omitempty"`
}

const subscriberBuffer = 32

// Hub 本地订阅注册表 + 可选 redis 桥
type Hub struct {
	mu      sync.Mutex
	local   map[string]map[chan Event]struct{}
	rdb     *redis.Client
	ps      map[string]*redis.PubSub
	closed  bool
}

// Default 进程级共享实例（api / worker 同包自动复用）
var Default = NewFromEnv()

func redisPassword() string {
	if pwd := os.Getenv("FORJA_REDIS_PASSWORD"); pwd != "" {
		return pwd
	}
	return os.Getenv("REDIS_PASSWORD")
}

func NewFromEnv() *Hub {
	h := &Hub{local: map[string]map[chan Event]struct{}{}, ps: map[string]*redis.PubSub{}}
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		rdb := redis.NewClient(&redis.Options{Addr: addr, Password: redisPassword()})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := rdb.Ping(ctx).Err()
		cancel()
		if err == nil {
			h.rdb = rdb
		} else {
			slog.Warn("events: redis unreachable, local-only mode", "err", err.Error())
			_ = rdb.Close()
		}
	}
	return h
}

func (h *Hub) ensureLocal(ch string) {
	if _, ok := h.local[ch]; !ok {
		h.local[ch] = map[chan Event]struct{}{}
	}
}

// Subscribe 订阅频道；返回只读事件流与幂等反注册函数。
func (h *Hub) Subscribe(channel string) (<-chan Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}
	h.ensureLocal(channel)
	c := make(chan Event, subscriberBuffer)
	h.local[channel][c] = struct{}{}
	if h.rdb != nil {
		h.ensureRedisSub(channel)
	}
	var once sync.Once
	unsub := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			delete(h.local[channel], c)
			close(c)
		})
	}
	return c, unsub
}

// ensureRedisSub 调用方需持锁；每频道一条 PubSub 连接，pump 到本地 fan-out
func (h *Hub) ensureRedisSub(channel string) {
	if _, ok := h.ps[channel]; ok {
		return
	}
	ps := h.rdb.Subscribe(context.Background(), channel)
	h.ps[channel] = ps
	go func() {
		for msg := range ps.Channel() {
			var evt Event
			if err := json.Unmarshal([]byte(msg.Payload), &evt); err != nil {
				continue
			}
			h.deliverLocal(channel, evt)
		}
	}()
}

func (h *Hub) deliverLocal(channel string, evt Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.local[channel] {
		select {
		case c <- evt:
		default: // 慢消费者：丢弃（脏通知语义，快照重拉兜底）；S4 #4a 起留痕可观测
			slog.Warn("event dropped for slow subscriber", "channel", channel, "event", evt.Name)
		}
	}
}

// Publish 发布事件到频道。redis 模式经 broker 回环（含本进程）；本地模式直接投递。
func (h *Hub) Publish(channel, name string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		b = nil
	}
	evt := Event{Name: name, Data: b}
	payload, _ := json.Marshal(evt)
	if h.rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := h.rdb.Publish(ctx, channel, payload).Err(); err != nil {
			slog.Warn("events publish failed", "channel", channel, "event", name, "err", err.Error())
		}
		return
	}
	h.deliverLocal(channel, evt)
}

// Close 释放资源（测试用）
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for ch := range h.local {
		for c := range h.local[ch] {
			close(c)
		}
	}
	h.local = map[string]map[chan Event]struct{}{}
	for k, ps := range h.ps {
		_ = ps.Close()
		delete(h.ps, k)
	}
	if h.rdb != nil {
		_ = h.rdb.Close()
		h.rdb = nil
	}
}
