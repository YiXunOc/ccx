package httpclient

import (
	"github.com/BenedictKing/ccx/internal/observability"
	"net/http"
)

// observedTransport wraps an explicit physical transport call. Retries inside
// net/http.Transport are intentionally outside this counter's scope.
type observedTransport struct{ next http.RoundTripper }

func (t observedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	o := observability.FromContext(req.Context())
	o.TransportStart()
	resp, err := t.next.RoundTrip(req)
	if resp != nil {
		o.Response(resp.StatusCode)
	}
	return resp, err
}
func observeTransport(t http.RoundTripper) http.RoundTripper {
	if d, ok := t.(*directFirstRoundTripper); ok {
		d.direct = observeTransport(d.direct)
		d.proxied = observeTransport(d.proxied)
		return d
	}
	return observedTransport{next: t}
}
