package observability

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
)

type CaptureMode string

const (
	CaptureModeOff     CaptureMode = "off"
	CaptureModeSummary CaptureMode = "summary"
	CaptureModeFailed  CaptureMode = "failed"
	CaptureModeAll     CaptureMode = "all"
)

func ParseCaptureMode(raw string) CaptureMode {
	switch CaptureMode(strings.ToLower(strings.TrimSpace(raw))) {
	case CaptureModeSummary:
		return CaptureModeSummary
	case CaptureModeFailed:
		return CaptureModeFailed
	case CaptureModeAll:
		return CaptureModeAll
	default:
		return CaptureModeOff
	}
}

type Context struct {
	CorrelationID     string `json:"requestCorrelationId"`
	TraceUID          string `json:"traceUid"`
	AttemptID         string `json:"attemptId"`
	TransportTryID    string `json:"transportTryId"`
	CaptureID         string `json:"captureId"`
	ChannelUID        string `json:"channelUid"`
	ClientProtocol    string `json:"clientProtocol"`
	ExecutionProtocol string `json:"executionProtocol"`
	Model             string `json:"model"`
	KeyMask           string `json:"keyMask"`
}

func NewContext(correlationID, traceUID, attemptID, channelUID, clientProtocol, executionProtocol, model, keyMask string) Context {
	return Context{
		CorrelationID: correlationID, TraceUID: traceUID, AttemptID: attemptID,
		CaptureID: newID("cap"), ChannelUID: channelUID,
		ClientProtocol: clientProtocol, ExecutionProtocol: executionProtocol,
		Model: model, KeyMask: keyMask,
	}
}

func (c Context) AssociationStatus() string {
	if c.CorrelationID == "" || c.AttemptID == "" || c.CaptureID == "" || c.TransportTryID == "" || c.ChannelUID == "" {
		return "incomplete"
	}
	return "complete"
}

type Summary struct {
	Context               Context        `json:"association"`
	AssociationStatus     string         `json:"associationStatus"`
	ClientBodyBytes       int            `json:"clientBodyBytes"`
	ConvertedBodyBytes    int            `json:"convertedBodyBytes"`
	OutboundBodyBytes     int            `json:"outboundBodyBytes"`
	ContentLength         int64          `json:"contentLength"`
	GetBodyReplayBytes    int            `json:"getBodyReplayBytes"`
	UpstreamStatus        int            `json:"upstreamStatus"`
	FirstUpstreamStatus   int            `json:"firstUpstreamStatus"`
	SyntheticFailure      bool           `json:"syntheticFailure"`
	HeaderCommitted       bool           `json:"headerCommitted"`
	ClientWriteBytes      int            `json:"clientWriteBytes"`
	EventCounts           map[string]int `json:"eventCounts,omitempty"`
	DownstreamEventCounts map[string]int `json:"downstreamEventCounts,omitempty"`
	Terminal              string         `json:"terminal,omitempty"`
	DownstreamTerminal    string         `json:"downstreamTerminal,omitempty"`
	Outcome               string         `json:"outcome,omitempty"`
	EOF                   bool           `json:"eof"`
	ReadError             string         `json:"readError,omitempty"`
	CaptureDropped        bool           `json:"captureDropped"`
	ReplayError           string         `json:"replayError,omitempty"`
	TransportTries        int            `json:"transportTries"`
	EmitterEvents         int            `json:"emitterEvents"`
	TransportScope        string         `json:"transportScope"`
}

func NewSummary(ctx Context) Summary {
	return Summary{Context: ctx, AssociationStatus: ctx.AssociationStatus(), ClientBodyBytes: -1, ConvertedBodyBytes: -1, OutboundBodyBytes: -1, ContentLength: -1, GetBodyReplayBytes: -1, ClientWriteBytes: -1, TransportScope: "explicit_roundtrip_excludes_net_http_internal_retries", EventCounts: make(map[string]int), DownstreamEventCounts: make(map[string]int)}
}

func ShouldRecord(mode string, failed bool) bool {
	switch ParseCaptureMode(mode) {
	case CaptureModeSummary, CaptureModeAll:
		return true
	case CaptureModeFailed:
		return failed
	default:
		return false
	}
}

func MarshalSummary(s Summary) ([]byte, error) {
	return json.Marshal(s)
}

func newID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + "_unavailable"
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
