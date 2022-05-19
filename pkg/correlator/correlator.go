package correlator

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
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
	Error       error `json:",omitempty"`
	Description string
	URL         string
}

type Input struct {
	AlertName      string
	IgnoreExemplar bool
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

	d = append(d, Discovery(fmt.Sprintf("Alert is indeed firing... 😱 Its labels: %v", alert.Labels)))

	lbl := alert.Labels.Clone()
	for predef := range alertRule.Labels {
		delete(lbl, predef)
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
	firstMatchers := selectors[0]

	var exampleRequestID string // Or traceID, same thing.
	if !input.IgnoreExemplar {
		// Get time range from expression.
		res, err := thanosAPI.QueryExemplars(ctx, alertRule.Query, time.Now().Add(-5*time.Minute), time.Now())
		if err != nil {
			return nil, nil, errors.Wrap(err, "exemplars")
		}

		if len(res) == 0 {
			level.Error(c.logger).Log("msg", "no exemplars found for series in question", "query", alertRule.Query)
		} else {
			level.Debug(c.logger).Log("msg", "found exemplars, taking first", "len", len(res), "query", alertRule.Query)

			var exRes v1.ExemplarQueryResult
			for _, r := range res {
				match := true
				for _, m := range firstMatchers {
					l, ok := r.SeriesLabels[model.LabelName(m.Name)]
					if !ok {
						continue
					}
					if !m.Matches(string(l)) {
						match = false
						break
					}
				}
				if !match {
					continue
				}

				exRes = r
				break
			}

			if len(exRes.Exemplars) == 0 {
				level.Error(c.logger).Log("msg", "no exemplars matching ):", "matchers", fmt.Sprintf("%v", firstMatchers))
			} else {
				level.Debug(c.logger).Log("msg", "found exemplar", "series", exRes.SeriesLabels, "labels", exRes.Exemplars[0].Labels)

				//for _, r := range res {
				//	for _, e := range r.Exemplars {
				//		fmt.Println(e.Labels)
				//	}
				//	fmt.Println(r.SeriesLabels)
				//}

				// TODO(bwplotka): Un-hardcode traceID key.
				exampleRequestID = string(exRes.Exemplars[0].Labels["traceID"])
				if exampleRequestID == "" {
					level.Error(c.logger).Log("msg", "no traceID key in labels")
				} else {
					d = append(d, Discovery(fmt.Sprintf("We found example Trace/Request ID for you! %v 🤗", exampleRequestID)))

				}
			}
		}
	}

	// Thanos Metrics view.

	// TODO(bwplotka): Create lib for building query?
	strMatchers := make([]string, 0, 10)
	for _, matcher := range selectors[0] {
		strMatchers = append(strMatchers, matcher.String())
	}

	corr = append(corr, Correlation{
		Description: "Metric View in Thanos",
		URL: "http://" + c.cfg.Sources.Thanos.ExternalEndpoint +
			`/graph?g0.expr=` + url.QueryEscape(fmt.Sprintf("{%s}", strings.Join(strMatchers, ","))) +
			`&g0.tab=0&g0.stacked=0&g0.range_input=15m&g0.max_source_resolution=0s`,
	})

	// Exemplars path.
	if exampleRequestID != "" {
		corr = append(corr, Correlation{
			Description: "Log View connected to that request ID in Loki via Grafana",
			URL:         "http://" + c.cfg.Sources.Loki.UISource.ExternalEndpoint + "/trace/" + exampleRequestID,
		})
		corr = append(corr, Correlation{
			Description: "Trace View in Jaeger",
			URL:         "http://" + c.cfg.Sources.Jaeger.ExternalEndpoint + "/trace/" + exampleRequestID,
		})
		return d, corr, nil
	}

	return d, corr, nil
}
