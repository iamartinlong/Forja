package api

// 覆盖率冲刺与本地/CI 集成测试的环境守卫：
// createFurnace/patch/delete 会异步调用 furnace.Ensure/Stop/Remove 真操作 Docker
// （CI backend job 与本地 go test 均注入 DOCKER_HOST），测试清理只删 DB 行，
// 容器必残留 → 2026-09-18/19 宿主出现 6+ 个 forja-furnace-* 孤儿（已实证与
// 每次 CI 派发时间一一对应）。TestMain 统一关闭该副作用，生产不受影响。
import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("FORJA_FURNACE_DISABLE_DOCKER", "1")
	os.Exit(m.Run())
}
