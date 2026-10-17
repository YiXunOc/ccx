package httpclient

import (
	"github.com/BenedictKing/ccx/internal/observability"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestObservedDirectFirstCountsActualRoundTrips(t *testing.T) {
	direct := &stubTransport{status: 403}
	proxy := &stubTransport{status: 200}
	o := observability.NewObserver("summary", observability.NewContext("c", "r", "a", "ch", "chat", "chat", "", ""), nil)
	req, _ := http.NewRequest("POST", "http://local.test", strings.NewReader("PRIVATE"))
	req = observability.Bind(req, o)
	rt := observeTransport(&directFirstRoundTripper{direct: direct, proxied: proxy})
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	s := o.Snapshot()
	if s.TransportTries != 2 || s.Context.TransportTryID == "" || s.UpstreamStatus != 200 {
		t.Fatalf("bad transport mapping: %+v", s)
	}
	if direct.calls != 1 || proxy.calls != 1 || string(proxy.lastBody) != "PRIVATE" {
		t.Fatal("transport changed replay")
	}
	data, _ := io.ReadAll(req.Body)
	_ = data
}
