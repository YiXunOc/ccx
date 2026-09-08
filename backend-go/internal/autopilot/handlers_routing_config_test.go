package autopilot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func setupRoutingConfigRouter(deps *RoutingConfigDeps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	group := r.Group("/api")
	RegisterRoutingConfigRoutes(group, deps)
	return r
}

func newRoutingConfigTestManager(t *testing.T, killSwitch bool) (*config.ConfigManager, string) {
	t.Helper()
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.json")
	autopilotConfig := config.DefaultAutopilotRoutingConfig()
	autopilotConfig.KillSwitch = killSwitch
	autopilotConfig.CostPreference.Mode = "balanced"
	autopilotConfig.Scenario.Mode = "auto"
	data, err := json.Marshal(config.Config{AutopilotRouting: autopilotConfig})
	if err != nil {
		t.Fatalf("序列化测试配置失败: %v", err)
	}
	if err := os.WriteFile(configFile, data, 0600); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	manager, err := config.NewConfigManager(configFile, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("创建 ConfigManager 失败: %v", err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager, configFile
}

func performRoutingConfigRequest(router http.Handler, method, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/smart-routing/config", bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func decodeRoutingConfigResponse(t *testing.T, recorder *httptest.ResponseRecorder) RoutingConfigResponse {
	t.Helper()
	var response RoutingConfigResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析 RoutingConfigResponse 失败: %v; body=%s", err, recorder.Body.String())
	}
	return response
}

func TestGetRoutingConfig_DefaultMode(t *testing.T) {
	cfg := config.DefaultAutopilotRoutingConfig()
	if cfg.EffectiveRoutingMode() != config.AutopilotModeAuto {
		t.Fatalf("默认应为 Autopilot 自动运行态，实际 %q", cfg.EffectiveRoutingMode())
	}
}

func TestPutRoutingConfig_InvalidMode(t *testing.T) {
	var req RoutingConfigUpdateRequest
	if err := json.Unmarshal([]byte(`{"mode":"shadow","costPreference":"wrong_value"}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.CostPreference != "wrong_value" {
		t.Fatalf("costPreference = %q", req.CostPreference)
	}
}

func TestIsTruthyEnv(t *testing.T) {
	tests := []struct {
		name     string
		val      string
		expected bool
	}{
		{"true", "true", true},
		{"TRUE", "TRUE", true},
		{"1", "1", true},
		{"yes", "yes", true},
		{"on", "on", true},
		{"false", "false", false},
		{"0", "0", false},
		{"no", "no", false},
		{"off", "off", false},
		{"empty", "", false},
		{"whitespace", "  true  ", true},
		{"random", "xyz", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTruthyEnv(tt.val)
			if result != tt.expected {
				t.Fatalf("isTruthyEnv(%q) = %v, 期望 %v", tt.val, result, tt.expected)
			}
		})
	}
}

func TestRoutingConfigResponse_Serialization(t *testing.T) {
	resp := RoutingConfigResponse{
		KillSwitchActive:     true,
		KillSwitchConfigured: false,
		KillSwitchForced:     true,
		CostPreference:       "balanced",
		L2ProbeEnabled:       true,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if _, exists := parsed["mode"]; exists {
		t.Fatal("配置响应不应再暴露 mode")
	}
	if parsed["killSwitchActive"] != true || parsed["killSwitchConfigured"] != false || parsed["killSwitchForced"] != true || parsed["costPreference"] != "balanced" || parsed["l2ProbeEnabled"] != true {
		t.Fatalf("序列化结果异常: %+v", parsed)
	}
}

func TestRoutingConfigUpdateRequest_Binding(t *testing.T) {
	var req RoutingConfigUpdateRequest
	r := httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(`{"mode":"off","costPreference":"quality_first","killSwitch":false}`))
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&req); err != nil {
		t.Fatalf("解码失败: %v", err)
	}
	if req.CostPreference != "quality_first" {
		t.Fatalf("期望 costPreference=quality_first, 实际=%s", req.CostPreference)
	}
	if req.KillSwitch == nil || *req.KillSwitch {
		t.Fatalf("JSON false 应绑定为非 nil 的 false 指针，实际=%v", req.KillSwitch)
	}
}

func TestGetRoutingConfigReportsKillSwitchStates(t *testing.T) {
	t.Setenv("AUTOPILOT_KILL_SWITCH", "true")
	manager, _ := newRoutingConfigTestManager(t, false)
	router := setupRoutingConfigRouter(&RoutingConfigDeps{CfgManager: manager})

	recorder := performRoutingConfigRequest(router, http.MethodGet, "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET status=%d, body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeRoutingConfigResponse(t, recorder)
	if !response.KillSwitchActive || response.KillSwitchConfigured || !response.KillSwitchForced {
		t.Fatalf("GET KillSwitch 三态异常: %+v", response)
	}
}

func TestPutRoutingConfigPersistsKillSwitchAndReportsStates(t *testing.T) {
	t.Setenv("AUTOPILOT_KILL_SWITCH", "false")
	manager, configFile := newRoutingConfigTestManager(t, false)
	router := setupRoutingConfigRouter(&RoutingConfigDeps{CfgManager: manager})

	for _, enabled := range []bool{true, false} {
		body := `{"killSwitch":false}`
		if enabled {
			body = `{"killSwitch":true}`
		}
		recorder := performRoutingConfigRequest(router, http.MethodPut, body)
		if recorder.Code != http.StatusOK {
			t.Fatalf("PUT killSwitch=%v status=%d, body=%s", enabled, recorder.Code, recorder.Body.String())
		}
		response := decodeRoutingConfigResponse(t, recorder)
		if response.KillSwitchActive != enabled || response.KillSwitchConfigured != enabled || response.KillSwitchForced {
			t.Fatalf("PUT killSwitch=%v 三态响应异常: %+v", enabled, response)
		}
		if got := manager.GetPersistedAutopilotRouting().KillSwitch; got != enabled {
			t.Fatalf("PUT killSwitch=%v 后内存持久化值=%v", enabled, got)
		}
		data, err := os.ReadFile(configFile)
		if err != nil {
			t.Fatalf("读取配置文件失败: %v", err)
		}
		var saved config.Config
		if err := json.Unmarshal(data, &saved); err != nil {
			t.Fatalf("解析配置文件失败: %v", err)
		}
		if saved.AutopilotRouting.KillSwitch != enabled {
			t.Fatalf("PUT killSwitch=%v 后磁盘持久化值=%v", enabled, saved.AutopilotRouting.KillSwitch)
		}
	}
}

func TestPutRoutingConfigForcedFalseRejectsAtomically(t *testing.T) {
	t.Setenv("AUTOPILOT_KILL_SWITCH", "true")
	manager, configFile := newRoutingConfigTestManager(t, false)
	router := setupRoutingConfigRouter(&RoutingConfigDeps{CfgManager: manager})
	beforeConfig := manager.GetPersistedAutopilotRouting()
	beforeDisk, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("读取请求前配置失败: %v", err)
	}

	recorder := performRoutingConfigRequest(router, http.MethodPut, `{"killSwitch":false,"costPreference":"quality_first","scenario":"hard_problem"}`)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("强制急停时 PUT false status=%d, want 409; body=%s", recorder.Code, recorder.Body.String())
	}
	afterConfig := manager.GetPersistedAutopilotRouting()
	if afterConfig.KillSwitch != beforeConfig.KillSwitch || afterConfig.CostPreference.Mode != beforeConfig.CostPreference.Mode || afterConfig.Scenario.Mode != beforeConfig.Scenario.Mode {
		t.Fatalf("409 应原子拒绝所有配置修改: before=%+v after=%+v", beforeConfig, afterConfig)
	}
	afterDisk, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("读取请求后配置失败: %v", err)
	}
	if !bytes.Equal(afterDisk, beforeDisk) {
		t.Fatal("409 原子拒绝不应修改磁盘配置")
	}
}

func TestPutRoutingConfigOtherFieldsDoNotChangeKillSwitch(t *testing.T) {
	t.Setenv("AUTOPILOT_KILL_SWITCH", "false")
	manager, _ := newRoutingConfigTestManager(t, true)
	router := setupRoutingConfigRouter(&RoutingConfigDeps{CfgManager: manager})

	for _, body := range []string{`{"costPreference":"quality_first"}`, `{"scenario":"hard_problem"}`} {
		recorder := performRoutingConfigRequest(router, http.MethodPut, body)
		if recorder.Code != http.StatusOK {
			t.Fatalf("PUT %s status=%d, body=%s", body, recorder.Code, recorder.Body.String())
		}
		if !manager.GetPersistedAutopilotRouting().KillSwitch {
			t.Fatalf("PUT %s 不应隐式关闭 KillSwitch", body)
		}
	}

	empty := performRoutingConfigRequest(router, http.MethodPut, `{}`)
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("空请求 status=%d, want 400; body=%s", empty.Code, empty.Body.String())
	}
}

func TestPutRoutingConfigRejectsAutoBeforeReadiness(t *testing.T) {
	t.Skip("Autopilot 唯一自动运行态不再存在 readiness 准入门槛")
}

func TestPutRoutingConfigManualAssistCancelsPendingAutoRecovery(t *testing.T) {
	t.Skip("Autopilot 唯一自动运行态不再支持 assist 降级或自动恢复")
}
