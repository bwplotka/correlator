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
// TODO(bwplotka): Compose it better, it's currently a too long function with hardcoded elements for demo purposes.
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
	var exRes v1.ExemplarQueryResult
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

	query := fmt.Sprintf("{%s}", strings.Join(strMatchers, ","))
	for _, matcher := range selectors[0] {
		if matcher.Name == "__name__" {
			if strings.HasSuffix(matcher.Value, "_total") {
				query = "rate(" + query + "[1m])"
			}
		}
	}

	corr = append(corr, Correlation{
		Description: "Metric View for the source of Alert [Thanos]",
		URL: "http://" + c.cfg.Sources.Thanos.ExternalEndpoint +
			`/graph?g0.expr=` + url.QueryEscape(query) +
			`&g0.tab=0&g0.stacked=0&g0.range_input=15m&g0.max_source_resolution=0s&` +
			`g1.expr=` + url.QueryEscape(strings.TrimSuffix(alertRule.Query, " > 0.3")) +
			`&g1.tab=0&g1.stacked=0&g1.range_input=15m&g1.max_source_resolution=0s&`,
	})

	// Exemplars path.
	if exampleRequestID != "" {
		corr = append(corr, Correlation{
			Description: "Log View connected to the Exemplar [Loki via Grafana]",
			// TODO(bwplotka): yolo - unhardcode!
			URL: "http://" + c.cfg.Sources.Loki.UISource.ExternalEndpoint +
				`/explore?orgId=1&left=%5B%22now-1h%22,%22now%22,%22Logging%22,%7B%22refId%22:%22A%22,%22expr%22:%22%7Bjobs%3D%5C%22` +
				string(exRes.SeriesLabels["job"]) + `%5C%22%7D%20%7C%3D%20%5C%22` + exampleRequestID + `%5C%22%5Cn%22%7D%5D`,
		})
		corr = append(corr, Correlation{
			Description: "Trace View connected to the Exemplar [Jaeger]",
			URL:         "http://" + c.cfg.Sources.Jaeger.ExternalEndpoint + "/trace/" + exampleRequestID,
		})
		// TODO(bwplotka) Check if Parca is configured!
		// TODO(bwplotka): Parse time!
		corr = append(corr, Correlation{
			Description: "Profiles View for the same container and time [Parca]",
			URL: "http://" + c.cfg.Sources.Parca.ExternalEndpoint +
				`/?currentProfileView=icicle&expression_a=process_cpu%3Acpu%3Ananoseconds%3Acpu%3Ananoseconds%3Adelta%7B` +
				`%20job%3D%22` + "e2e-correlation-" + string(alert.Labels["job"]) + `%3A8080%22%7D&merge_a=true&time_selection_a=relative:hour|1`,
		})
		// TODO(bwplotka): Parca storage not always is able to find trace label. Some sampling is happening?
		corr = append(corr, Correlation{
			Description: "Experimental: Profiles View connected to the Exemplar [Parca]",
			URL: "http://" + c.cfg.Sources.Parca.ExternalEndpoint +
				`/?currentProfileView=icicle&expression_a=process_cpu%3Acpu%3Ananoseconds%3Acpu%3Ananoseconds%3Adelta%7Bprofile_label_trace_id%3D%22` +
				exampleRequestID + `%22%2C%20job%3D%22` + "e2e-correlation-" + string(exRes.SeriesLabels["job"]) + `%3A8080%22%7D&merge_a=true&time_selection_a=relative:hour|1`,
		})

		return d, corr, nil
	}

	corr = append(corr, Correlation{
		Description: "Log View for the same container and time [Loki via Grafana]",
		// TODO(bwplotka): yolo - unhardcode!
		URL: "http://" + c.cfg.Sources.Loki.UISource.ExternalEndpoint +
			`/explore?orgId=1&left=%5B%22now-1h%22,%22now%22,%22Logging%22,%7B%22refId%22:%22A%22,%22expr%22:%22%7Bjobs%3D%5C%22` + string(alert.Labels["job"]) + `%5C%22%7D%22%7D%5D`,
	})
	corr = append(corr, Correlation{
		Description: "Trace View for the same container and time [Jaeger]",
		URL:         "http://" + c.cfg.Sources.Jaeger.ExternalEndpoint + "/search?end=1653036151287000&limit=20&lookback=1h&maxDuration&minDuration&service=demo%3Aping&start=1653032551287000",
	})
	corr = append(corr, Correlation{
		Description: "Profiles View for the same container and time [Parca]",
		URL: "http://" + c.cfg.Sources.Parca.ExternalEndpoint +
			`/?currentProfileView=icicle&expression_a=process_cpu%3Acpu%3Ananoseconds%3Acpu%3Ananoseconds%3Adelta%7B` +
			`%20job%3D%22` + "e2e-correlation-" + string(alert.Labels["job"]) + `%3A8080%22%7D&merge_a=true&time_selection_a=relative:hour|1`,
	})
	return d, corr, nil
}
