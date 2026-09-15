package common

import (
	"net/http"
	"testing"
)

// 协议端点不支持信号识别测试。
// 正例来自 2026-09-12 seekai-cc 实测错误体（glm-5.3-flash 被替代映射到
// responses 端点后上游明确拒绝），负例守住防误杀红线：通用 invalid_request、
// 模型不存在（not_found）不得学成协议能力结论。

func TestProtocolEndpointUnsupportedFromError(t *testing.T) {
	tests := []struct {
		name string
		code int
		body string
		want bool
	}{
		{
			name: "seekai 实测形态 code+message",
			code: http.StatusBadRequest,
			body: `{"error":{"message":"model \"glm-5.3-flash\" is not supported on /v1/responses; use /v1/chat/completions instead (request id: 20260912024248381576692c955d568HVVS3vjK)","type":"invalid_request_error","param":"","code":"model_not_supported_on_endpoint"}}`,
			want: true,
		},
		{
			name: "仅 code 强信号",
			code: http.StatusBadRequest,
			body: `{"error":{"message":"bad request","code":"model_not_supported_on_endpoint"}}`,
			want: true,
		},
		{
			name: "顶层 code",
			code: http.StatusBadRequest,
			body: `{"message":"bad request","code":"model_not_supported_on_endpoint"}`,
			want: true,
		},
		{
			name: "仅文案无 code",
			code: http.StatusBadRequest,
			body: `{"error":{"message":"model \"gpt-x\" is not supported on /v1/responses"}}`,
			want: true,
		},
		{
			name: "反向文案：端点不支持模型",
			code: http.StatusBadRequest,
			body: `{"error":{"message":"/v1/responses does not support model glm-5.3-flash"}}`,
			want: true,
		},
		{
			name: "模型不存在不学",
			code: http.StatusBadRequest,
			body: `{"error":{"message":"The model ` + "`glm-x`" + ` does not exist or you do not have access to it.","type":"invalid_request_error","code":"model_not_found"}}`,
			want: false,
		},
		{
			name: "通用 invalid_request 不学",
			code: http.StatusBadRequest,
			body: `{"error":{"message":"Invalid value for 'temperature'","type":"invalid_request_error"}}`,
			want: false,
		},
		{
			name: "422 不学",
			code: http.StatusUnprocessableEntity,
			body: `{"error":{"message":"model \"glm-5.3-flash\" is not supported on /v1/responses"}}`,
			want: false,
		},
		{
			name: "空体",
			code: http.StatusBadRequest,
			body: ``,
			want: false,
		},
		{
			name: "非 JSON",
			code: http.StatusBadRequest,
			body: `<html>bad gateway</html>`,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ProtocolEndpointUnsupportedFromError(tt.code, []byte(tt.body))
			if (got != nil) != tt.want {
				t.Fatalf("ProtocolEndpointUnsupportedFromError() = %v, want命中=%v", got, tt.want)
			}
			if got != nil && got.Evidence == "" {
				t.Fatal("命中时 Evidence 不应为空")
			}
		})
	}
}

func TestProtocolUnsupportedLearningTrait(t *testing.T) {
	if got := string(ProtocolUnsupportedLearningTrait("responses")); got != "no_protocol_support:responses" {
		t.Fatalf("trait 键 = %q", got)
	}
	// 空白与大写归一
	if got := string(ProtocolUnsupportedLearningTrait(" Chat ")); got != "no_protocol_support:chat" {
		t.Fatalf("归一后 trait 键 = %q", got)
	}
}
