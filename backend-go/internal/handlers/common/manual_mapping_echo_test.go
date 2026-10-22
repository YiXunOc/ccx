package common

import (
	"encoding/json"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestManualMappingEchoSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Config{AutopilotRouting: config.DefaultAutopilotRoutingConfig()}
	cfg.AutopilotRouting.ModelMapping.EchoMappedModel = true
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("mappingSource", "manual_mapping")
	writeEchoMappingHeaders(c, manager, "B", "B", "A")
	if got := recorder.Header().Get("X-CCX-Mapping-Source"); got != "manual_mapping" {
		t.Fatalf("source=%q", got)
	}
}
