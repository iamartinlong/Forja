package events

// P1 SSE 事件总线单测：本地模式投递 / 慢消费者丢弃 / 幂等反订阅。
// redis 模式依赖 REDIS_ADDR——CI/本地有 redis 时自动覆盖（Subscribe→Publish 回环）。

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHub_LocalPubSub(t *testing.T) {
	h := NewFromEnv() // 无 REDIS_ADDR → 本地模式
	defer h.Close()

	ch, unsub := h.Subscribe("task:t1")
	require.NotNil(t, ch)

	h.Publish("task:t1", "status", map[string]any{"status": "RUNNING"})

	select {
	case evt, ok := <-ch:
		require.True(t, ok)
		assert.Equal(t, "status", evt.Name)
		var data map[string]any
		require.NoError(t, json.Unmarshal(evt.Data, &data))
		assert.Equal(t, "RUNNING", data["status"])
	case <-time.After(2 * time.Second):
		t.Fatal("未收到事件")
	}

	// 幂等反订阅 + 关闭后再收不到
	unsub()
	unsub()
	_, ok := <-ch
	assert.False(t, ok, "unsub 后 channel 应关闭")
}

func TestHub_SlowConsumerDrop(t *testing.T) {
	h := &Hub{local: map[string]map[chan Event]struct{}{}}
	c := make(chan Event, 1)
	h.local["x"] = map[chan Event]struct{}{c: {}}
	for i := 0; i < 10; i++ { // 超过缓冲不阻塞、不 panic
		h.deliverLocal("x", Event{Name: "n"})
	}
	assert.Len(t, c, 1)
}

func TestHub_UnsubscribedNoDelivery(t *testing.T) {
	h := NewFromEnv()
	defer h.Close()
	_, unsub := h.Subscribe("task:gone")
	unsub()
	h.Publish("task:gone", "status", nil) // 不应 panic / 泄漏
}
