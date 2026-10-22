package common

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/BenedictKing/ccx/internal/config"
)

// manualModelAvailable is nil when discovery has not been initialized (unknown => attempt).
var manualModelAvailable func(channelUID, baseURL, apiKey, target string) bool

// SetManualModelAvailabilityProvider checks known per-key inventories; absent inventories permit attempts.
func SetManualModelAvailabilityProvider(check func(channelUID, baseURL, apiKey, target string) bool) {
	manualModelAvailable = check
}

// manualTargetRejected identifies a model-specific upstream refusal. Only manual
// mapping changes retry classification; unrelated invalid requests stay terminal.
func manualTargetRejected(status int, body []byte) bool {
	return status >= 400 && status < 500 && keyModelRestrictionReason(body) != ""
}

func manualAttemptModel(upstream *config.UpstreamConfig, original string) string {
	if target := upstream.ManualModel(original); target != "" {
		return target
	}
	return original
}

// prepareManualAttemptBody sets the target before model-specific preprocessing and
// provider conversion. Gemini ingress has no top-level model: its model is in the URL.
func prepareManualAttemptBody(body []byte, target string) ([]byte, error) {
	if target == "" || !gjson.GetBytes(body, "model").Exists() {
		return body, nil
	}
	return sjson.SetBytes(body, "model", target)
}

// Rewrite after protocol conversion: Gemini uses the model in the URL; all other
// ingress/egress combinations (including multipart images) carry it in the body.
func rewriteManualProviderRequest(req *http.Request, target string) error {
	if req == nil || target == "" {
		return nil
	}
	if strings.Contains(req.URL.Path, "/models/") {
		parts := strings.SplitN(req.URL.Path, "/models/", 2)
		if len(parts) == 2 && strings.Contains(parts[1], ":") {
			suffix := parts[1][strings.Index(parts[1], ":"):]
			req.URL.Path = parts[0] + "/models/" + target + suffix
			req.URL.RawPath = parts[0] + "/models/" + url.PathEscape(target) + suffix
		}
	}
	if req.Body == nil {
		return nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}
	req.Body.Close()
	contentType := req.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		_, params, parseErr := mime.ParseMediaType(contentType)
		if parseErr != nil {
			return parseErr
		}
		reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		var output bytes.Buffer
		writer := multipart.NewWriter(&output)
		for {
			part, nextErr := reader.NextPart()
			if nextErr == io.EOF {
				break
			}
			if nextErr != nil {
				return nextErr
			}
			dst, createErr := writer.CreatePart(part.Header)
			if createErr != nil {
				return createErr
			}
			if part.FormName() == "model" {
				if _, createErr = io.Copy(io.Discard, part); createErr != nil {
					return createErr
				}
				_, createErr = io.WriteString(dst, target)
			} else {
				_, createErr = io.Copy(dst, part)
			}
			if createErr != nil {
				return createErr
			}
		}
		if err := writer.Close(); err != nil {
			return err
		}
		body = output.Bytes()
		req.Header.Set("Content-Type", writer.FormDataContentType())
	}
	if gjson.GetBytes(body, "model").Exists() {
		body, err = sjson.SetBytes(body, "model", target)
		if err != nil {
			return err
		}
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	return nil
}
