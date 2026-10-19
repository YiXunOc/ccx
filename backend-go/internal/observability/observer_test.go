package observability

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
)

func TestReadErrorFiniteCategories(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{io.ErrUnexpectedEOF, "unexpected_eof"},
		{context.Canceled, "cancelled"},
		{context.DeadlineExceeded, "timeout"},
		{&net.OpError{Op: "read", Err: errors.New("PRIVATE")}, "network_error"},
		{errors.New("PRIVATE"), "read_error"},
	} {
		o := NewObserver("summary", Context{}, nil)
		o.ReadEnd(tc.err)
		if got := o.Snapshot().ReadError; got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestObserverModesUnknownAndReadBoundaries(t *testing.T) {
	for _, mode := range []string{"off", "summary", "failed", "all"} {
		for _, failed := range []bool{false, true} {
			calls := 0
			o := NewObserver(mode, NewContext("c", "r", "a", "ch", "responses", "responses", "", ""), func(s Summary) {
				calls++
				if s.OutboundBodyBytes != -1 || s.GetBodyReplayBytes != -1 {
					t.Fatalf("metadata is not measurement: %+v", s)
				}
			})
			req, _ := http.NewRequest("POST", "http://local.test", nil)
			req.ContentLength = 999
			o.Request(req)
			o.Data(`{"type":"response.metadata","prompt":"PRIVATE"}`)
			if failed {
				o.Data(`{"type":"response.failed","response":{"error":{"message":"PRIVATE"}}}`)
				o.ReadEnd(errors.New("PRIVATE"))
			} else {
				o.Data(`{"type":"response.completed"}`)
				o.ReadEnd(io.EOF)
			}
			o.Finish("result", failed, false, 0)
			o.Finish("duplicate", failed, false, 0)
			want := 0
			if ShouldRecord(mode, failed) {
				want = 1
			}
			if calls != want {
				t.Fatalf("mode=%s failed=%t calls=%d", mode, failed, calls)
			}
			if o != nil {
				s := o.Snapshot()
				if s.EOF == failed || s.EventCounts["response.metadata"] != 1 {
					t.Fatalf("bad boundary: %+v", s)
				}
				data, _ := MarshalSummary(s)
				if strings.Contains(string(data), "PRIVATE") {
					t.Fatal("payload leak")
				}
			}
		}
	}
}
func TestObserverConcurrentCountersAndSnapshots(t *testing.T) {
	o := NewObserver("summary", NewContext("c", "r", "a", "ch", "chat", "chat", "", ""), nil)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				o.Event("PRIVATE-unlisted")
				o.Snapshot()
				o.Emitter()
			}
		}()
	}
	wg.Wait()
	s := o.Snapshot()
	if s.EventCounts["other"] != 2000 || len(s.EventCounts) != 1 || s.EmitterEvents != 2000 {
		t.Fatalf("lost/unbounded events: %+v", s)
	}
	s.EventCounts["other"] = 0
	if o.Snapshot().EventCounts["other"] != 2000 {
		t.Fatal("mutable snapshot")
	}
}

type failingReplay struct{}

func (failingReplay) Read(p []byte) (int, error) { copy(p, "abc"); return 3, errors.New("PRIVATE") }
func (failingReplay) Close() error               { return nil }
func TestObserverReplayReadErrorAndConversionRewrite(t *testing.T) {
	o := NewObserver("summary", Context{}, nil)
	req, _ := http.NewRequest("POST", "http://local.test", nil)
	req.GetBody = func() (io.ReadCloser, error) { return failingReplay{}, nil }
	req = WithConvertedBytes(req, []byte("before"))
	req = WithConvertedBytes(req, []byte("after"))
	o.Request(req)
	s := o.Snapshot()
	if s.ReplayError != "replay_read_error" || s.GetBodyReplayBytes != 3 || s.ConvertedBodyBytes != 5 || s.OutboundBodyBytes != -1 {
		t.Fatalf("bad replay: %+v", s)
	}
}

func TestObserverRequestKeepsFirstRoundMetadata(t *testing.T) {
	o := NewObserver("summary", Context{}, nil)
	first, _ := http.NewRequest("POST", "http://local.test", nil)
	first.ContentLength = 11
	first = WithConvertedBytes(first, []byte("first"))
	o.Request(first)
	second, _ := http.NewRequest("POST", "http://local.test", nil)
	second.ContentLength = 22
	second = WithConvertedBytes(second, []byte("second-round"))
	o.Request(second)
	s := o.Snapshot()
	if s.ContentLength != 11 || s.ConvertedBodyBytes != 5 {
		t.Fatalf("request metadata was overwritten: %+v", s)
	}
}

func TestObserverFinishFailOpenAndTerminalClassification(t *testing.T) {
	o := NewObserver("failed", NewContext("c", "r", "a", "ch", "responses", "responses", "", ""), func(s Summary) {
		if s.Outcome != "upstream_terminal_failure" {
			t.Errorf("contradictory outcome: %+v", s)
		}
		panic("writer failure")
	})
	o.Event("response.failed")
	o.Event("done")
	o.Finish("success", false, false, 0)
	if got := o.Snapshot().Terminal; got != "response.failed" {
		t.Fatalf("DONE overwrote failure: %q", got)
	}
	o.Finish("duplicate", true, false, 0)
	req, _ := http.NewRequest("POST", "http://local.test", nil)
	req.GetBody = func() (io.ReadCloser, error) { return nil, nil }
	o.Request(req)
	if o.Snapshot().ReplayError != "get_body_error" {
		t.Fatal("nil replay not classified")
	}
	ctx := NewContext("c", "r", "a", "", "responses", "responses", "", "")
	ctx.TransportTryID = "tx"
	if ctx.AssociationStatus() != "incomplete" {
		t.Fatal("missing channel treated as complete")
	}
}

func TestObserverSyntheticTerminalDoesNotMutateUpstreamCounts(t *testing.T) {
	o := NewObserver("summary", Context{}, nil)
	o.Response(200)
	o.Event("response.completed")
	o.MarkSyntheticTerminal()
	s := o.Snapshot()
	if s.Terminal != "response.incomplete" || s.EventCounts["response.incomplete"] != 0 || s.EventCounts["response.completed"] != 1 || s.UpstreamStatus != 200 {
		t.Fatalf("synthetic terminal corrupted upstream observation: %+v", s)
	}
}
