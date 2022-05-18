package correlator

import (
	"net/url"

	"github.com/pkg/errors"
)

type lokiSource struct {
	cfg LokiSource
}

func newLokiSource(s LokiSource) *lokiSource {
	return &lokiSource{
		cfg: s,
	}
}

func (s *lokiSource) InternalEndpoint() string {
	return s.InternalEndpoint()
}

func (s *lokiSource) ExternalEndpoint() string {
	return s.ExternalEndpoint()
}

func (s *lokiSource) ParseRequestFromURL(u *url.URL) (request, bool, error) {
	return nil, false, errors.New("not implemented")
}
