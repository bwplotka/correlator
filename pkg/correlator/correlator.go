package correlator

import (
	"context"
	"net/url"
)

type Correlator struct {
	cfg Config
}

func New(cfg Config) (*Correlator, error) {
	return &Correlator{
		cfg: cfg,
	}, nil
}

type Correlation struct {
	Error       error `json:"omitempty"`
	Description string
	URL         url.URL
}

// TODO(bwplotka): Make it a streaming response.
func (c *Correlator) CorrelateFromURL(ctx context.Context, url string) ([]Correlation, error) {

	return nil, nil
}
