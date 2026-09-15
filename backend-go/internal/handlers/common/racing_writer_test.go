package common

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

func newBranchTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	return c, recorder
}

// pre-commit 缓冲：头/状态码/体不得触达真实 writer。
func TestRacingBranchWriterBuffersBeforeCommit(t *testing.T) {
	c, recorder := newBranchTestContext()
	bw := newRacingBranchWriter(c.Writer)

	bw.Header().Set("X-Branch", "shadow")
	bw.WriteHeader(http.StatusAccepted)
	if _, err := bw.Write([]byte("chunk-1")); err != nil {
		t.Fatal(err)
	}
	bw.Flush() // pre-commit 无操作

	if recorder.Body.Len() != 0 || recorder.Header().Get("X-Branch") != "" {
		t.Fatalf("pre-commit 不得写入真实 writer: header=%v body=%q", recorder.Header(), recorder.Body.String())
	}
	if !bw.Written() || bw.Status() != http.StatusAccepted || bw.Size() != len("chunk-1") {
		t.Fatalf("分支本地视图不符: written=%v status=%d size=%d", bw.Written(), bw.Status(), bw.Size())
	}
}

// Commit 桥接：私有头（含多值）并入真实 writer，缓冲体回放，之后透传。
func TestRacingBranchWriterCommitBridges(t *testing.T) {
	c, recorder := newBranchTestContext()
	bw := newRacingBranchWriter(c.Writer)

	bw.Header().Add("X-Multi", "a")
	bw.Header().Add("X-Multi", "b")
	bw.WriteHeader(http.StatusCreated)
	_, _ = bw.Write([]byte("buffered"))
	bw.Commit()

	if got := recorder.Header().Values("X-Multi"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("多值头未完整并入: %v", got)
	}
	if recorder.Code != http.StatusCreated {
		t.Fatalf("状态码未回放: %d", recorder.Code)
	}
	if recorder.Body.String() != "buffered" {
		t.Fatalf("缓冲体未回放: %q", recorder.Body.String())
	}

	// 提交后透传：头直接落真实 map，体直接写
	bw.Header().Set("X-After", "direct")
	_, _ = bw.WriteString("-live")
	bw.Flush()
	if recorder.Header().Get("X-After") != "direct" || recorder.Body.String() != "buffered-live" {
		t.Fatalf("提交后应透传: header=%v body=%q", recorder.Header(), recorder.Body.String())
	}
	if !recorder.Flushed {
		t.Fatal("提交后 Flush 应透传")
	}
	if bw.Status() != http.StatusCreated || bw.Size() != len("buffered-live") || !bw.Written() {
		t.Fatalf("提交后应代理真实 writer 视图: status=%d size=%d", bw.Status(), bw.Size())
	}

	// 幂等
	bw.Commit()
	if recorder.Body.String() != "buffered-live" {
		t.Fatalf("重复 Commit 不得重放: %q", recorder.Body.String())
	}
}

// Discard 放弃写出权：缓冲丢弃，后续写出全部静默丢弃且不得触达真实 writer。
func TestRacingBranchWriterDiscard(t *testing.T) {
	c, recorder := newBranchTestContext()
	bw := newRacingBranchWriter(c.Writer)

	bw.Header().Set("X-Branch", "loser")
	_, _ = bw.Write([]byte("lost"))
	bw.Discard()

	n, err := bw.Write([]byte("late"))
	if n != len("late") || err != nil {
		t.Fatalf("败者 Write 应假装成功: n=%d err=%v", n, err)
	}
	bw.WriteHeader(http.StatusInternalServerError)
	bw.Flush()

	if recorder.Body.Len() != 0 || recorder.Header().Get("X-Branch") != "" || recorder.Code != http.StatusOK {
		t.Fatalf("败者不得触达真实 writer: code=%d header=%v body=%q", recorder.Code, recorder.Header(), recorder.Body.String())
	}

	// Discard 后 Commit 不得生效
	bw.Commit()
	if recorder.Body.Len() != 0 {
		t.Fatalf("Discard 后 Commit 不得写出: %q", recorder.Body.String())
	}
}

// 单写者不变量（替代原"共享主 Writer + 纪律"模型）：两条分支并发写，
// 只有 claim 赢家的字节到达真实 writer；输家的头不污染赢家响应。
func TestRacingBranchWriterSingleWinnerEnforcement(t *testing.T) {
	c, recorder := newBranchTestContext()
	winner := newRacingBranchWriter(c.Writer)
	loser := newRacingBranchWriter(c.Writer)

	loser.Header().Set("X-Who", "loser")
	winner.Header().Set("X-Who", "winner")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = winner.Write([]byte("W")) }()
		go func() { defer wg.Done(); _, _ = loser.Write([]byte("L")) }()
	}
	wg.Wait()

	loser.Discard()
	winner.Commit()

	// 赢家提交后，输家继续写（模拟被取消前的在飞写）也不得到达
	if n, _ := loser.Write([]byte("LATE")); n != 4 {
		t.Fatalf("败者写应假装成功: %d", n)
	}

	if recorder.Header().Get("X-Who") != "winner" {
		t.Fatalf("真实响应头应只含赢家: %v", recorder.Header())
	}
	body := recorder.Body.String()
	if body != "WWWWWWWW" {
		t.Fatalf("真实响应体应只含赢家字节: %q", body)
	}
}

// 未 commit 的分支 Hijack 返回错误而非 panic（竞速路径不武装 WS，防御兜底）。
func TestRacingBranchWriterHijackGuard(t *testing.T) {
	c, _ := newBranchTestContext()
	bw := newRacingBranchWriter(c.Writer)
	if _, _, err := bw.Hijack(); err == nil {
		t.Fatal("pre-commit Hijack 应返回错误")
	}
}
