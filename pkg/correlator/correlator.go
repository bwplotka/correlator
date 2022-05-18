package correlator

import (
	"context"
	"net/url"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/pkg/errors"
)

type Correlator struct {
	cfg     Config
	logger  log.Logger
	sources []source
}

type source interface {
	ParseRequestFromURL(u *url.URL) (request, bool, error)
	InternalEndpoint() string
	ExternalEndpoint() string
}

type request interface {
}

// Matcher metricLabelMatcher a matches the value of a given label.
type metricLabelMatcher struct {
	Name  string
	Value string
	Logic string
}

type metricRequest struct {
	Matchers  []metricLabelMatcher
	StartTime time.Time
	EndTime   time.Time
	Step      time.Duration
}

func New(cfg Config, logger log.Logger) (*Correlator, error) {
	var sources []source

	if (cfg.Sources.Thanos != ThanosSource{}) {
		sources = append(sources, newThanosSource(cfg.Sources.Thanos))
	}
	if (cfg.Sources.Loki != LokiSource{}) {
		sources = append(sources, newLokiSource(cfg.Sources.Loki))
	}
	if (cfg.Sources.Jaeger != JaegerSource{}) {
		sources = append(sources, newJaegerSource(cfg.Sources.Jaeger))
	}
	return &Correlator{
		cfg:     cfg,
		logger:  logger,
		sources: sources,
	}, nil
}

type Correlation struct {
	Error       error `json:";omitempty"`
	Description string
	URL         url.URL
}

// CorrelateFromURL provides correlations from the URL.
// NOTE: ARTIFICIAL INTELLIGENCE - USE WITH CARE!
// TODO(bwplotka): Make it a streaming response.
func (c *Correlator) CorrelateFromURL(ctx context.Context, urlOrPath string) ([]Correlation, error) {
	level.Debug(c.logger).Log("msg", "correlating from URL", "url", urlOrPath)

	return nil, nil
}

func (c *Correlator) findSource(ctx context.Context, urlOrPath string) error {
	u, err := url.Parse(urlOrPath)
	if err != nil {
		// Assuming path, not implemented yet.
		return errors.Wrap(err, "Could not parse an URL")
	}

	for _, s := range c.sources {
		_, ok, err := s.ParseRequestFromURL(u)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		// DO..
		//r.Type()
	}
	return errors.New("This AI is smart, but not smart enough - did not match this URL with known source ): ")
}
