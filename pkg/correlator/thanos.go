package correlator

import (
	"net"
	"net/url"
	"time"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/pkg/errors"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/promql/parser"
)

type thanosSource struct {
	cfg    ThanosSource
	logger log.Logger
}

func newThanosSource(s ThanosSource, logger log.Logger) *thanosSource {
	return &thanosSource{
		cfg:    s,
		logger: logger,
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
	selects []metricRequest
}

func (s *thanosSource) ParseRequestFromURL(u *url.URL) (request, bool, error) {
	// Get only g0 for now
	expr := u.Query().Get("g0.expr")
	if expr == "" {
		endpoint := net.JoinHostPort(u.Host, u.Port())
		if s.InternalEndpoint() == endpoint || s.ExternalEndpoint() == endpoint {
			return nil, true, errors.New("Can't deduce much, nothing was queried!")
		}
		return nil, false, nil
	}

	e, err := parser.ParseExpr(expr)
	if err != nil {
		return nil, true, errors.Wrapf(err, "parse g0.expr %v", expr)
	}

	startTime := time.Time{}
	endTime := time.Time{}

	// Figure out time.
	rangeInput := u.Query().Get("g0.range_input")
	if rangeInput != "" {
		d, err := model.ParseDuration(rangeInput)
		if err != nil {
			return nil, true, errors.Wrapf(err, "parse g0.range_input %v", rangeInput)
		}

		endTime = time.Now()
		startTime = endTime.Add(-1 * time.Duration(d))
	}

	if startTime == (time.Time{}) {
		level.Warn(s.logger).Log("msg", "couldn't figure time, assuming last 30 minutes")
		endTime = time.Now()
		startTime = endTime.Add(-30 * time.Minute)
	}

	// TODO(bwplotka): Parse step.
	r := &thanosUIRequest{}

	parser.Inspect(e, func(node parser.Node, nodes []parser.Node) error {
		switch n := node.(type) {
		case *parser.VectorSelector:
			if n.Timestamp != nil || n.StartOrEnd == parser.START || n.StartOrEnd == parser.END {
				// TODO: At modifier used, support that.

			}
			if n.OriginalOffset != 0 {
				// TODO: support that.
			}
			r.selects = append(r.selects, metricRequest{
				Matchers:  n.LabelMatchers,
				StartTime: startTime,
				EndTime:   endTime,
			})

		case *parser.MatrixSelector:
			vs := n.VectorSelector.(*parser.VectorSelector)
			if vs.Timestamp != nil || vs.StartOrEnd == parser.START || vs.StartOrEnd == parser.END {
				// TODO: At modifier used, support that.
			}
			if vs.OriginalOffset < 0 {
				// TODO: At modifier used, support that.
			}
			r.selects = append(r.selects, metricRequest{
				Matchers:  vs.LabelMatchers,
				StartTime: startTime,
				EndTime:   endTime,
			})
		}
		return nil
	})

	if len(r.selects) > 0 {

	}
	return nil, false, errors.New("not implemented")
}
