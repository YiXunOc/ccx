package common

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func TestRewriteManualProviderRequestPreservesJSONNumericPrecision(t *testing.T) {
	const payload = `{"model":"A","seed":9007199254740993,"input":"keep"}`
	req, err := http.NewRequest(http.MethodPost, "https://example.test/v1/chat/completions", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if err := rewriteManualProviderRequest(req, "B"); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != `{"model":"B","seed":9007199254740993,"input":"keep"}` {
		t.Fatalf("mapping altered unrelated JSON: %s", got)
	}
}

func TestRewriteManualProviderRequestJSONAndGeminiURL(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/responses", "/v1/chat/completions", "/v1/embeddings", "/v1/images/generations", "/v1beta/models/A:generateContent"} {
		req, err := http.NewRequest(http.MethodPost, "https://example.test"+path, strings.NewReader(`{"model":"A","input":"untouched"}`))
		if err != nil {
			t.Fatal(err)
		}
		if err := rewriteManualProviderRequest(req, "B"); err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(req.Body)
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		if data["model"] != "B" || data["input"] != "untouched" {
			t.Fatalf("%s body=%s", path, raw)
		}
		if strings.Contains(path, "/models/") && req.URL.Path != "/v1beta/models/B:generateContent" {
			t.Fatalf("URL=%s", req.URL.Path)
		}
	}
}

func TestRewriteManualProviderRequestMultipartPreservesBinary(t *testing.T) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	_ = w.WriteField("model", "A")
	p, err := w.CreateFormFile("image", "data.bin")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = p.Write([]byte{0, 1, 2, 255})
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, "https://example.test/v1/images/edits", &b)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if err := rewriteManualProviderRequest(req, "B"); err != nil {
		t.Fatal(err)
	}
	if err := req.ParseMultipartForm(1024); err != nil {
		t.Fatal(err)
	}
	if req.FormValue("model") != "B" {
		t.Fatalf("model=%q", req.FormValue("model"))
	}
	file, _, err := req.FormFile("image")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	content, _ := io.ReadAll(file)
	if !bytes.Equal(content, []byte{0, 1, 2, 255}) {
		t.Fatalf("binary changed: %v", content)
	}
}
