package correlator

import (
	"context"
	"net/url"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/prometheus/model/labels"
)

type Correlator struct {
	cfg    Config
	logger log.Logger
}

type metricRequest struct {
	Matchers  []*labels.Matcher
	StartTime time.Time
	EndTime   time.Time
}

func New(cfg Config, logger log.Logger) (*Correlator, error) {

	return &Correlator{
		cfg:    cfg,
		logger: logger,
	}, nil
}

type Correlation struct {
	Error       error `json:";omitempty"`
	Description string
	URL         url.URL
}

type Input struct {
	AlertName string
}

type Discoveries string

// Correlate provides correlations from the best effort input.
// NOTE: ARTIFICIAL INTELLIGENCE - USE WITH CARE!
// TODO(bwplotka): Make it a streaming response.
func (c *Correlator) Correlate(ctx context.Context, input Input) ([]Discoveries, []Correlation, error) {
	level.Debug(c.logger).Log("msg", "correlating from Input", "input", input)

	if input.AlertName == "" {
		return nil, nil, errors.New("not enough information")
	}
	thanosClient, err := api.NewClient(api.Config{
		Address: c.cfg.Sources.Thanos.InternalEndpoint,
	})
	if err != nil {
		return nil, nil, errors.Wrap(err, "new Thanos HTTP client")
	}

	level.Debug(c.logger).Log("msg", "calling rules API")

	thanosAPI := v1.NewAPI(thanosClient)
	rules, err := thanosAPI.Rules(ctx)
	if err != nil {
		return nil, nil, errors.Wrap(err, "rules")
	}

	var alertRule v1.AlertingRule
groupLoop:
	for _, g := range rules.Groups {
		for _, r := range g.Rules {
			switch v := r.(type) {
			case v1.AlertingRule:
				if v.Name == input.AlertName {
					alertRule = v
					break groupLoop
				}
			}
		}
	}

	level.Debug(c.logger).Log("msg", "calling rules API", "alert", alertRule)

	//level.Debug(c.logger).Log("msg", "Querying Exemplars")
	//thanosAPI.QueryExemplars(ctx, )
	//if err != nil {
	//	return nil, nil, errors.Wrap(err, "exemplars")
	//}
	return nil, nil, errors.New("not implemented")
}
