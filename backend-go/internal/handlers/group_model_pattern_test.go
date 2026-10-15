package handlers

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

func TestGroupModelPatternManagement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, kind := range []string{"Messages", "Chat", "Responses", "Gemini", "Images", "Vectors"} {
		t.Run(kind, func(t *testing.T) {
			up := []config.UpstreamConfig{{Name: "management-pattern", BaseURL: "https://test.invalid", ServiceType: "openai", APIKeys: []string{"test-key"}}}
			cfg := config.Config{Upstream: up, ChatUpstream: up, ResponsesUpstream: up, GeminiUpstream: up, ImagesUpstream: up, VectorsUpstream: up}
			data, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			cm, err := config.NewConfigManager(path, "")
			if err != nil {
				t.Fatal(err)
			}
			defer cm.Close()
			router := gin.New()
			router.POST("/channels/:id/disable", DisableGroupModel(cm, kind))
			router.POST("/channels/:id/restore", RestoreGroupModel(cm, kind))
			request := func(action, pattern string, want int) {
				t.Helper()
				body, _ := json.Marshal(map[string]string{"apiKey": "test-key", "model": pattern})
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/channels/0/"+action, bytes.NewReader(body)))
				if w.Code != want {
					t.Fatalf("%s %q status=%d body=%s", action, pattern, w.Code, w.Body.String())
				}
			}
			for _, pattern := range []string{"GPT-*", "*-mini", "*preview*", "*", "gpt-5"} {
				request("disable", pattern, 200)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			request("disable", " gpt-* ", 200)
			for _, pattern := range []string{"gpt-?", "[ab]", "!gpt-*", "gpt*mini", "**"} {
				request("disable", pattern, 400)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("duplicate or invalid management request changed persisted rules")
			}
			request("restore", "gpt-5", 200)
			request("restore", " gPt-* ", 200)
			request("restore", "gpt-*", 200)
		})
	}
}
