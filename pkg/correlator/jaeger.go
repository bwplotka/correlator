package correlator

import (
	"net/url"

	"github.com/pkg/errors"
)

type jaegerSource struct {
	cfg JaegerSource
}

func newJaegerSource(s JaegerSource) *jaegerSource {
	return &jaegerSource{
		cfg: s,
	}
}

func (s *jaegerSource) InternalEndpoint() string {
	return s.InternalEndpoint()
}

func (s *jaegerSource) ExternalEndpoint() string {
	return s.ExternalEndpoint()
}

func (s *jaegerSource) ParseRequestFromURL(u *url.URL) (request, bool, error) {
	return nil, false, errors.New("not implemented")
}
