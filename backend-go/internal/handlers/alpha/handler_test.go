package alpha

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
)

func setupAlphaTestConfigManager(t *testing.T, upstreams []config.UpstreamConfig) *config.ConfigManager {
	t.Helper()
	cfg := config.Config{ResponsesUpstream: upstreams}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("serialize config: %v", err)
	}
	tmpFile := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	cm, err := config.NewConfigManager(tmpFile, "")
	if err != nil {
		t.Fatalf("NewConfigManager() err = %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })
	return cm
}

func newAlphaTestRouter(t *testing.T, upstreams []config.UpstreamConfig) (*gin.Engine, *scheduler.ChannelScheduler) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfgManager := setupAlphaTestConfigManager(t, upstreams)
	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	traceAffinity := session.NewTraceAffinityManager()

	t.Cleanup(func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		chatMetrics.Stop()
		imagesMetrics.Stop()
		traceAffinity.Stop()
	})

	sch := scheduler.NewChannelScheduler(
		cfgManager,
		messagesMetrics,
		responsesMetrics,
		geminiMetrics,
		chatMetrics,
		imagesMetrics,
		traceAffinity,
		nil,
	)

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret-key",
		MaxRequestBodySize: 1024 * 1024,
	}

	r := gin.New()
	handler := Handler(envCfg, cfgManager, sch)
	r.POST("/v1/alpha/*rest", handler)
	r.POST("/:routePrefix/v1/alpha/*rest", handler)
	return r, sch
}

func performAlphaRequest(t *testing.T, router *gin.Engine, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "secret-key")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// capturedRequest 记录上游收到的请求
type capturedRequest struct {
	path   string
	header http.Header
	body   string
}

type captureServer struct {
	*httptest.Server
	mu        sync.Mutex
	requests  []capturedRequest
	status    int
	response  string
	failFirst int // 前 N 个请求返回 500 触发故障转移
}

func newCaptureServer(t *testing.T, status int, response string) *captureServer {
	t.Helper()
	cs := &captureServer{status: status, response: response}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cs.mu.Lock()
		cs.requests = append(cs.requests, capturedRequest{path: r.URL.Path, header: r.Header.Clone(), body: string(body)})
		count := len(cs.requests)
		failFirst := cs.failFirst
		cs.mu.Unlock()
		if count <= failFirst {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"upstream boom"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(cs.status)
		_, _ = io.WriteString(w, cs.response)
	}))
	t.Cleanup(cs.Close)
	return cs
}

func (cs *captureServer) requestCount() int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return len(cs.requests)
}

func (cs *captureServer) last() capturedRequest {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(cs.requests) == 0 {
		return capturedRequest{}
	}
	return cs.requests[len(cs.requests)-1]
}

func TestAlphaHandler_URLBuilding(t *testing.T) {
	tests := []struct {
		name         string
		baseURLMaker func(serverURL string) string
		requestPath  string
		wantUpstream string
	}{
		{
			name:         "base_with_version_tail",
			baseURLMaker: func(u string) string { return u + "/v1" },
			requestPath:  "/v1/alpha/history/v2/list_windows",
			wantUpstream: "/v1/alpha/history/v2/list_windows",
		},
		{
			name:         "base_without_version",
			baseURLMaker: func(u string) string { return u },
			requestPath:  "/v1/alpha/notes/v2/write_file",
			wantUpstream: "/v1/alpha/notes/v2/write_file",
		},
		{
			name:         "base_with_hash_suffix",
			baseURLMaker: func(u string) string { return u + "/custom#" },
			requestPath:  "/v1/alpha/history/v2/read_item",
			wantUpstream: "/custom/alpha/history/v2/read_item",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newCaptureServer(t, 200, `{"ok":true}`)
			router, _ := newAlphaTestRouter(t, []config.UpstreamConfig{{
				Name:        "url-test",
				BaseURL:     tt.baseURLMaker(server.URL),
				APIKeys:     []string{"sk-test"},
				ServiceType: "responses",
				Status:      "active",
			}})
			w := performAlphaRequest(t, router, tt.requestPath, `{"context":{"session_id":"s1"}}`, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
			}
			if got := server.last().path; got != tt.wantUpstream {
				t.Fatalf("upstream path = %q, want %q", got, tt.wantUpstream)
			}
		})
	}
}

func TestAlphaHandler_HeaderPassthroughAndAuthSwap(t *testing.T) {
	server := newCaptureServer(t, 200, `{"windows":[]}`)
	router, _ := newAlphaTestRouter(t, []config.UpstreamConfig{{
		Name:        "hdr-test",
		BaseURL:     server.URL + "/v1",
		APIKeys:     []string{"sk-channel-key"},
		ServiceType: "responses",
		Status:      "active",
	}})

	w := performAlphaRequest(t, router, "/v1/alpha/notes/v2/search_contents",
		`{"query":"deploy","context":{"session_id":"s1"}}`,
		map[string]string{
			"x-openai-encrypted-tool-arguments":      "true",
			"x-openai-tool-output-truncation-policy": `{"type":"output_tokens","max_tokens":512}`,
		})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}

	got := server.last()
	if got.header.Get("x-openai-encrypted-tool-arguments") != "true" {
		t.Error("encrypted-tool-arguments header must be forwarded verbatim")
	}
	if !strings.Contains(got.header.Get("x-openai-tool-output-truncation-policy"), "output_tokens") {
		t.Error("truncation-policy header must be forwarded verbatim")
	}
	if auth := got.header.Get("Authorization"); auth != "Bearer sk-channel-key" {
		t.Errorf("Authorization = %q, want channel key", auth)
	}
	if got.header.Get("x-api-key") != "" {
		t.Error("client x-api-key must not leak upstream")
	}
	if !strings.Contains(got.body, `"session_id":"s1"`) {
		t.Errorf("body must be forwarded verbatim, got %s", got.body)
	}
}

func TestAlphaHandler_404PassthroughOnExhaustion(t *testing.T) {
	// 单渠道 404：候选耗尽后必须如实回写 404（上游未实现 alpha），而非误导性 503
	server := newCaptureServer(t, http.StatusNotFound, `{"error":"not found"}`)
	router, _ := newAlphaTestRouter(t, []config.UpstreamConfig{{
		Name:        "no-alpha",
		BaseURL:     server.URL + "/v1",
		APIKeys:     []string{"sk-test"},
		ServiceType: "responses",
		Status:      "active",
	}})

	w := performAlphaRequest(t, router, "/v1/alpha/history/v2/list_windows", `{"context":{"session_id":"s1"}}`, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (truthful passthrough), body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not found") {
		t.Errorf("upstream error body must be forwarded, got %s", w.Body.String())
	}
}

func TestAlphaHandler_FailoverAcrossChannels(t *testing.T) {
	// 双渠道：第一渠道 500（可故障转移），第二渠道成功
	failing := newCaptureServer(t, http.StatusInternalServerError, `{"error":"boom"}`)
	healthy := newCaptureServer(t, 200, `{"windows":["w1"]}`)
	router, _ := newAlphaTestRouter(t, []config.UpstreamConfig{
		{Name: "first", BaseURL: failing.URL + "/v1", APIKeys: []string{"sk-1"}, ServiceType: "responses", Status: "active"},
		{Name: "second", BaseURL: healthy.URL + "/v1", APIKeys: []string{"sk-2"}, ServiceType: "responses", Status: "active"},
	})

	w := performAlphaRequest(t, router, "/v1/alpha/history/v2/list_windows", `{"context":{"session_id":"s1"}}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if failing.requestCount() != 1 || healthy.requestCount() != 1 {
		t.Fatalf("failing=%d healthy=%d, want 1/1", failing.requestCount(), healthy.requestCount())
	}
}

func TestAlphaHandler_SessionStickyAndBucketSweep(t *testing.T) {
	high := newCaptureServer(t, 200, `{"src":"high"}`)
	low := newCaptureServer(t, 200, `{"src":"low"}`)
	router, sch := newAlphaTestRouter(t, []config.UpstreamConfig{
		// Priority 显式配置：0 会被配置加载判为「未配置」并分配 11，故用 1/10 区分
		{Name: "high-priority", BaseURL: high.URL + "/v1", APIKeys: []string{"sk-1"}, ServiceType: "responses", Status: "active", Priority: 1},
		{Name: "low-priority", BaseURL: low.URL + "/v1", APIKeys: []string{"sk-2"}, ServiceType: "responses", Status: "active", Priority: 10},
	})

	// 1. session 亲和（无桶键，SelectChannel 内部读取）：预写低优先级渠道的亲和，
	//    请求应命中它而非更高优先级渠道
	sch.SetTraceAffinity("sess-affinity", 1, scheduler.ChannelKindResponses)
	w := performAlphaRequest(t, router, "/v1/alpha/history/v2/list_windows", `{"context":{"session_id":"sess-affinity"}}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"low"`) {
		t.Fatalf("affinity pin failed: status=%d body=%s", w.Code, w.Body.String())
	}

	// 2. 桶扫描 pin：推理请求按 ctx-200k 桶写亲和，无桶读取扫到后应命中
	requirement := &scheduler.ContextRequirement{InputTokens: 200000}
	sch.SetTraceAffinityForRequirement("sess-sweep", 1, scheduler.ChannelKindResponses, requirement)
	w = performAlphaRequest(t, router, "/v1/alpha/history/v2/list_items", `{"context":{"session_id":"sess-sweep"}}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"low"`) {
		t.Fatalf("bucket sweep pin failed: status=%d body=%s", w.Code, w.Body.String())
	}

	// 3. 无亲和的请求走正常优先级 → 高优先级渠道
	w = performAlphaRequest(t, router, "/v1/alpha/history/v2/list_items", `{"context":{"session_id":"fresh"}}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"high"`) {
		t.Fatalf("normal selection failed: status=%d body=%s", w.Code, w.Body.String())
	}

	// 4. 成功后写入 alpha 自身亲和：sess-affinity 再来一次仍命中同一渠道
	w = performAlphaRequest(t, router, "/v1/alpha/history/v2/list_items", `{"context":{"session_id":"sess-affinity"}}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"low"`) {
		t.Fatalf("self-affinity failed: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAlphaHandler_XChannelPin(t *testing.T) {
	high := newCaptureServer(t, 200, `{"src":"high"}`)
	pinned := newCaptureServer(t, 200, `{"src":"pinned"}`)
	router, _ := newAlphaTestRouter(t, []config.UpstreamConfig{
		{Name: "high-priority", BaseURL: high.URL + "/v1", APIKeys: []string{"sk-1"}, ServiceType: "responses", Status: "active"},
		{Name: "pinned-channel", BaseURL: pinned.URL + "/v1", APIKeys: []string{"sk-2"}, ServiceType: "responses", Status: "active"},
	})

	w := performAlphaRequest(t, router, "/v1/alpha/notes/v2/read_file", `{"path":"main/notes/x"}`, map[string]string{
		"X-Channel": "pinned-channel",
	})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"pinned"`) {
		t.Fatalf("X-Channel pin failed: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAlphaHandler_AuthRejected(t *testing.T) {
	server := newCaptureServer(t, 200, `{"ok":true}`)
	router, _ := newAlphaTestRouter(t, []config.UpstreamConfig{{
		Name: "auth-test", BaseURL: server.URL + "/v1", APIKeys: []string{"sk-test"}, ServiceType: "responses", Status: "active",
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/alpha/history/v2/list_windows", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "wrong-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if server.requestCount() != 0 {
		t.Error("unauthenticated request must not reach upstream")
	}
}
