package common

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

// racingBranchWriter 竞速分支的独立响应写出端。
//
// pre-commit 阶段把响应头/状态码/体缓冲在分支私有状态里；claim 赢家经 Commit()
// 一次性桥接到真实客户端 writer 并转为透传；败者 Discard() 后全部写出操作静默
// 丢弃（Write 返回 len(p), nil，不干扰上游流读取）。
//
// 不变量由构造保证：真实 writer 只被 claim 赢家触碰，分支间不共享响应头 map——
// 取代此前"影子直接回填主 Writer + 各协议 handler 自觉遵守 claim 纪律"的约定式
// 单写者模型（2026-09-08 双 panic 事故的临时修复形态）。
type racingBranchWriter struct {
	real gin.ResponseWriter

	mu        sync.Mutex
	header    http.Header
	status    int
	buf       []byte
	committed bool
	discarded bool
}

var _ gin.ResponseWriter = (*racingBranchWriter)(nil)

func newRacingBranchWriter(real gin.ResponseWriter) *racingBranchWriter {
	return &racingBranchWriter{real: real, header: make(http.Header)}
}

// Commit 桥接到真实 writer：回放私有响应头与已缓冲状态/体，之后全部操作透传。
// 仅 claim 赢家调用（racingClaimClientCommit 裁决点）；幂等。
func (w *racingBranchWriter) Commit() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.committed || w.discarded {
		return
	}
	dst := w.real.Header()
	for k, vs := range w.header {
		cp := make([]string, len(vs))
		copy(cp, vs)
		dst[k] = cp
	}
	if w.status != 0 {
		w.real.WriteHeader(w.status)
	}
	w.committed = true
	if len(w.buf) > 0 {
		_, _ = w.real.Write(w.buf)
		w.buf = nil
	}
}

// Discard 放弃写出权：丢弃缓冲，后续全部写出操作变为无操作。仅 claim 败者
// 调用（racingClaimClientCommit 裁决点）；幂等。
func (w *racingBranchWriter) Discard() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.discarded = true
	w.buf = nil
}

func (w *racingBranchWriter) Header() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.committed {
		return w.real.Header()
	}
	return w.header
}

func (w *racingBranchWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.discarded {
		return
	}
	if w.committed {
		w.real.WriteHeader(status)
		return
	}
	// 与 gin responseWriter 同语义：写出前最后一次 WriteHeader 生效
	w.status = status
}

func (w *racingBranchWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.discarded {
		return len(p), nil
	}
	if w.committed {
		return w.real.Write(p)
	}
	w.buf = append(w.buf, p...)
	return len(p), nil
}

func (w *racingBranchWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *racingBranchWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.committed && !w.discarded {
		w.real.Flush()
	}
}

func (w *racingBranchWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.committed {
		return w.real.Status()
	}
	if w.status != 0 {
		return w.status
	}
	return http.StatusOK
}

func (w *racingBranchWriter) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.committed {
		return w.real.Size()
	}
	if len(w.buf) == 0 {
		return -1 // 对齐 gin 未写出时的 noWritten
	}
	return len(w.buf)
}

func (w *racingBranchWriter) Written() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.committed {
		return w.real.Written()
	}
	return w.status != 0 || len(w.buf) > 0
}

func (w *racingBranchWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.discarded {
		return
	}
	if w.committed {
		w.real.WriteHeaderNow()
		return
	}
	// pre-commit 无法真正写出（头尚未桥接），置默认状态码标记写出意图
	if w.status == 0 {
		w.status = http.StatusOK
	}
}

// Hijack 仅提交后的赢家可用（竞速只武装 messages/chat/responses/gemini
// 的 HTTP/SSE 路径，正常不会触达）；pre-commit 返回错误而非崩 panic。
func (w *racingBranchWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.committed || w.discarded {
		return nil, nil, errors.New("racing branch writer: hijack requires committed branch")
	}
	return w.real.Hijack()
}

// CloseNotify 直接代理真实 writer：客户端断开是连接级信号，与分支提交状态无关。
func (w *racingBranchWriter) CloseNotify() <-chan bool {
	return w.real.CloseNotify()
}

func (w *racingBranchWriter) Pusher() http.Pusher {
	return w.real.Pusher()
}
