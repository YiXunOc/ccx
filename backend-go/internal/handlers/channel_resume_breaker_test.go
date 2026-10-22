package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

func TestResumeChannelDashboardClosesCombinedBreakerAndKeepsHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.Config{Upstream: []config.UpstreamConfig{{
		Name: "combined", ServiceType: "openai", BaseURLs: []string{"https://primary.example.com", "https://backup.example.com"},
		APIKeys: []string{"key-a", "key-b"}, Status: "active",
	}}}
	path := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := config.NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	mm := metrics.NewMetricsManager()
	defer mm.Stop()
	mm.UpdateCircuitBreakerConfig(metrics.CircuitBreakerParams{WindowSize: 50, FailureThreshold: 0.85, ConsecutiveFailuresThreshold: 10})
	affinity := session.NewTraceAffinityManager()
	defer affinity.Stop()
	sch := scheduler.NewChannelScheduler(cm, mm, metrics.NewMetricsManager(), metrics.NewMetricsManager(), metrics.NewMetricsManager(), metrics.NewMetricsManager(), affinity, warmup.NewURLManager(30*time.Second, 3))
	urls := cm.GetConfig().Upstream[0].GetAllBaseURLs()
	keys := cm.GetConfig().Upstream[0].APIKeys
	for i := 0; i < 10; i++ {
		mm.RecordFailure(urls[i%2], keys[(i/2)%2], "openai", "")
	}
	r := gin.New()
	r.GET("/dashboard", GetChannelDashboard(cm, sch))
	r.POST("/messages/channels/:id/resume", ResumeChannel(sch, cm, false))
	dashboardState := func() (string, float64) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard?type=messages", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("dashboard status=%d", w.Code)
		}
		var body struct {
			Metrics []struct {
				CircuitState string  `json:"circuitState"`
				FailureCount float64 `json:"failureCount"`
			} `json:"metrics"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Metrics) != 1 {
			t.Fatalf("metrics count=%d", len(body.Metrics))
		}
		return body.Metrics[0].CircuitState, body.Metrics[0].FailureCount
	}
	if state, _ := dashboardState(); state != "open" {
		t.Fatalf("before resume=%s", state)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/messages/channels/0/resume", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("resume status=%d body=%s", w.Code, w.Body.String())
	}
	if state, failures := dashboardState(); state != "closed" || failures != 10 {
		t.Fatalf("after resume state=%s failures=%v, want closed/10", state, failures)
	}
}
