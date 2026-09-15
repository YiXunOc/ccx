package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/BenedictKing/ccx/internal/config"
)

// ── 渠道兼容性能力记忆查看/清除 API ──
//
// ChannelCompatCache 落盘在 .config/channel_compat.json，此前没有任何 HTTP 端点，
// 运维查看只能读原始 JSON、清除只能删文件后重启。本组端点提供只读快照与
// 按 trait 粒度的清除（无需重启，立即生效并落盘）。
//
// 快照包含三个分区：entries（渠道×Key×模型的 traits/上下文收紧/输出上限）、
// contextWindows（渠道×协议×模型的放宽方向窗口证据）与
// latencyPenalties（渠道×Key×模型×任务类的延迟慢证据，竞速负反馈学习）。清除粒度：
//   - DELETE ?trait=<name>：只清该 trait，其他分区不受影响；
//   - DELETE ?section=context-window：只清窗口分区；
//   - DELETE ?section=latency-penalty：只清延迟慢证据分区；
//   - DELETE（无参数）：全部分区。

// CompatCacheResponse GET /api/compat-cache 返回结构。
type CompatCacheResponse struct {
	Entries          []config.CompatSnapshotEntry         `json:"entries"`
	ContextWindows   []config.ContextWindowSnapshotEntry  `json:"contextWindows"`
	LatencyPenalties []config.LatencyPenaltySnapshotEntry `json:"latencyPenalties"`
	Total            int                                  `json:"total"`
}

// ClearCompatCacheResponse DELETE /api/compat-cache 返回结构。
type ClearCompatCacheResponse struct {
	Removed int    `json:"removed"`
	Trait   string `json:"trait,omitempty"`   // 空 = 非按 trait 清除
	Section string `json:"section,omitempty"` // context-window = 只清窗口分区
}

// compatCacheSectionContextWindow 显式的窗口分区清除参数值。
// 不复用 trait 名称空间，避免与既有 trait 冲突。
const compatCacheSectionContextWindow = "context-window"

// compatCacheSectionLatencyPenalty 延迟慢证据分区清除参数值。
const compatCacheSectionLatencyPenalty = "latency-penalty"

// RegisterCompatCacheRoutes 注册兼容性记忆管理端点到给定路由组。
func RegisterCompatCacheRoutes(router gin.IRouter) {
	group := router.Group("/compat-cache")
	{
		group.GET("", handleListCompatCache)
		group.DELETE("", handleClearCompatCache)
	}
}

func handleListCompatCache(c *gin.Context) {
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		c.JSON(http.StatusOK, CompatCacheResponse{
			Entries:          []config.CompatSnapshotEntry{},
			ContextWindows:   []config.ContextWindowSnapshotEntry{},
			LatencyPenalties: []config.LatencyPenaltySnapshotEntry{},
			Total:            0,
		})
		return
	}
	entries := cache.Snapshot()
	if entries == nil {
		entries = []config.CompatSnapshotEntry{}
	}
	windows := cache.ContextWindowSnapshot()
	if windows == nil {
		windows = []config.ContextWindowSnapshotEntry{}
	}
	latency := cache.LatencyPenaltySnapshot()
	if latency == nil {
		latency = []config.LatencyPenaltySnapshotEntry{}
	}
	c.JSON(http.StatusOK, CompatCacheResponse{
		Entries:          entries,
		ContextWindows:   windows,
		LatencyPenalties: latency,
		Total:            len(entries),
	})
}

func handleClearCompatCache(c *gin.Context) {
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		c.JSON(http.StatusOK, ClearCompatCacheResponse{})
		return
	}
	trait := config.CompatTrait(c.Query("trait"))
	section := c.Query("section")

	if section == compatCacheSectionContextWindow {
		// 只清窗口分区；与 trait 参数互斥（section 优先判定，trait 忽略）。
		c.JSON(http.StatusOK, ClearCompatCacheResponse{
			Removed: cache.ClearContextWindows(),
			Section: compatCacheSectionContextWindow,
		})
		return
	}
	if section == compatCacheSectionLatencyPenalty {
		// 只清延迟慢证据分区。
		c.JSON(http.StatusOK, ClearCompatCacheResponse{
			Removed: cache.ClearLatencyPenalties(),
			Section: compatCacheSectionLatencyPenalty,
		})
		return
	}
	removed := cache.ClearTrait(trait)
	if trait != "" {
		// ClearTrait 空参数即全清（含窗口分区）；这里显式区分两种语义便于响应回显。
		c.JSON(http.StatusOK, ClearCompatCacheResponse{Removed: removed, Trait: string(trait)})
		return
	}
	c.JSON(http.StatusOK, ClearCompatCacheResponse{Removed: removed})
}
