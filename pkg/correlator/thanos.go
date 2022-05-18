package correlator

import (
	"net"
	"net/url"

	"github.com/pkg/errors"
)

type thanosSource struct {
	cfg ThanosSource
}

func newThanosSource(s ThanosSource) *thanosSource {
	return &thanosSource{
		cfg: s,
	}
}

func (s *thanosSource) InternalEndpoint() string {
	return s.InternalEndpoint()
}

func (s *thanosSource) ExternalEndpoint() string {
	return s.ExternalEndpoint()
}

// For example: // http://127.0.0.1:49157/graph?g0.expr=rate(http_requests_total%7Bhandler!%3D%22%2Fping%22%2C%20instance%3D%22e2e-correlation-ping-eu1-valencia%3A8080%22%7D%5B5m%5D)&g0.tab=0&g0.stacked=0&g0.range_input=15m&g0.max_source_resolution=0s&g0.deduplicate=1&g0.partial_response=0&g0.store_matches=%5B%5D
type thanosUIRequest struct {
	queries []metricRequest
}

func (s *thanosSource) ParseRequestFromURL(u *url.URL) (request, bool, error) {
	endpoint := net.JoinHostPort(u.Host, u.Port())
	if s.InternalEndpoint() == endpoint || s.ExternalEndpoint() == endpoint {
		// Definitely Thanos.
	}

	// Get g0 for now!
	u.Query().Get("g0.expr")

	//promql.NewEngine()

	return nil, false, errors.New("not implemented")
}
