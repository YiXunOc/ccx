package channels

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

func TestManualModelMappingCreateUpdatePhysicalPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data, _ := json.Marshal(config.Config{})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := config.NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r.Group("/api"), cm, nil)
	call := func(method, url, body string) {
		t.Helper()
		req := httptest.NewRequest(method, url, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, url, w.Code, w.Body.String())
		}
	}
	call(http.MethodPost, "/api/channels", `{"kind":"messages","baseUrl":"https://example.test","apiKeys":["test-key"],"modelMapping":{"A":"B"}}`)
	// Reload triggers legacy UID backfill for newly created physical cards.
	_ = cm.Close()
	cm, err = config.NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	r = gin.New()
	RegisterRoutes(r.Group("/api"), cm, nil)
	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 1 || cfg.Upstream[0].ManualModel("A") != "B" {
		t.Fatalf("create=%+v", cfg.Upstream)
	}
	uid := cfg.Upstream[0].ChannelUID
	call(http.MethodPut, "/api/channels/"+uid, `{"modelMapping":{"X":"Y"}}`)
	if got := cm.GetConfig().Upstream[0]; got.ManualModel("X") != "Y" || got.ManualModel("A") != "" {
		t.Fatalf("update=%+v", got.ModelMapping)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"modelMapping": {`)) || !bytes.Contains(raw, []byte(`"X": "Y"`)) {
		t.Fatalf("disk missing mapping: %s", raw)
	}
}
