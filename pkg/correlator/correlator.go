package correlator

import (
	"context"
	"fmt"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/prometheus/promql/parser"
)

type Correlator struct {
	cfg    Config
	logger log.Logger
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
	URL         string
}

type Input struct {
	AlertName string
}

type Discovery string

// Correlate provides correlations from the best effort input.
// NOTE: ARTIFICIAL INTELLIGENCE - USE WITH CARE!
// TODO(bwplotka): Make it a streaming response.
func (c *Correlator) Correlate(ctx context.Context, input Input) (d []Discovery, corr []Correlation, _ error) {
	level.Debug(c.logger).Log("msg", "correlating from Input", "input", fmt.Sprintf("%v", input))

	if input.AlertName == "" {
		return nil, nil, errors.New("not enough information")
	}
	thanosClient, err := api.NewClient(api.Config{
		Address: "http://" + c.cfg.Sources.Thanos.InternalEndpoint,
	})
	if err != nil {
		return nil, nil, errors.Wrap(err, "new Thanos HTTP client")
	}

	thanosAPI := v1.NewAPI(thanosClient)
	rules, err := thanosAPI.Rules(ctx)
	if err != nil {
		return nil, nil, errors.Wrap(err, "rules")
	}

	var alert *v1.Alert
	var alertRule v1.AlertingRule

groupLoop:
	for _, g := range rules.Groups {
		for _, r := range g.Rules {
			switch v := r.(type) {
			case v1.AlertingRule:
				if v.Name == input.AlertName {
					if len(v.Alerts) == 0 {
						return nil, nil, errors.Errorf("requested alert no longer fires, alertname: %v", input.AlertName)
					}
					alert = v.Alerts[0]
					alertRule = v
					break groupLoop
				}
			}
		}
	}

	level.Debug(c.logger).Log("msg", "found firing alert", "alert", alert.Labels)

	lbl := alert.Labels.Clone()
	for predef := range alertRule.Labels {
		delete(lbl, predef)
	}

	var exampleRequestID string // Or traceID, same thing.
	{
		// Get time range from expression.
		res, err := thanosAPI.QueryExemplars(ctx, alertRule.Query, time.Now().Add(-5*time.Minute), time.Now())
		if err != nil {
			return nil, nil, errors.Wrap(err, "exemplars")
		}

		if len(res) == 0 {
			level.Error(c.logger).Log("no exemplars found for series in question")
		} else {
			level.Debug(c.logger).Log("msg", "found exemplars, taking first", "len", len(res))
			// TODO(bwplotka): Un-hardcode trace id.

			exampleRequestID = string(res[0].Exemplars[0].Labels["trace-id"])
			d = append(d, Discovery(fmt.Sprintf("Example Trace/Request ID: %v", exampleRequestID)))
		}
	}

	expr, err := parser.ParseExpr(alertRule.Query)
	if err != nil {
		return nil, nil, err
	}
	selectors := parser.ExtractSelectors(expr)

	// TODO(bwplotka): Support more than one.
	if len(selectors) == 0 {
		return nil, nil, errors.Errorf("can find selectors for %v", alertRule.Query)
	}

	var expression string
	corr = append(corr, Correlation{
		Description: "Metric View in Thanos",
		URL:         "http://" + c.cfg.Sources.Thanos.ExternalEndpoint + `/graph?g0.expr=` + expression + `&g0.tab=0&g0.stacked=0&g0.range_input=15m&g0.max_source_resolution=0s`,
	})

	return d, corr, nil
}
