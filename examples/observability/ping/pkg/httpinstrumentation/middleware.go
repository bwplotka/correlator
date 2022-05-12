package httpinstrumentation

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/bwplotka/correlator/examples/observability/ping/pkg/httpinstrumentation/logging"
	"github.com/bwplotka/tracing-go/tracing"
	tracinghttp "github.com/bwplotka/tracing-go/tracing/http"
	"github.com/go-kit/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/trace"
)

// Middleware auto instruments net/http HTTP handlers with:
// * Prometheus metrics + exemplars
// * Logging
// * Tracing + propagation
type Middleware interface {
	// WrapHandler wraps the given HTTP handler for instrumentation.
	WrapHandler(handlerName string, handler http.Handler) http.HandlerFunc
}

type nopMiddleware struct{}

func (ins nopMiddleware) WrapHandler(_ string, handler http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}
}

// NewNopMiddleware provides a Middleware which does nothing.
func NewNopMiddleware() Middleware {
	return nopMiddleware{}
}

type middleware struct {
	reg             prometheus.Registerer
	logMiddleware   *logging.HTTPMiddleware
	traceMiddleware *tracinghttp.Middleware

	buckets []float64
}

// NewMiddleware provides default Middleware.
// Passing nil as buckets uses the default buckets.
func NewMiddleware(reg prometheus.Registerer, buckets []float64, logger log.Logger, tracer *tracing.Tracer) Middleware {
	if buckets == nil {
		buckets = []float64{0.001, 0.01, 0.1, 0.3, 0.6, 1, 3, 6, 9, 20, 30, 60, 90, 120, 240, 360, 720}
	}

	return &middleware{reg: reg, buckets: buckets, logMiddleware: logging.NewHTTPServerMiddleware(logger), traceMiddleware: tracinghttp.NewMiddleware(tracer)}
}

// WrapHandler wraps the given HTTP handler for instrumentation:
// * It registers four metric collectors (if not already done) and reports HTTP
// metrics to the (newly or already) registered collectors: http_requests_total
// (CounterVec), http_request_duration_seconds (Histogram),
// http_request_size_bytes (Summary), http_response_size_bytes (Summary). Each
// has a constant label named "handler" with the provided handlerName as
// value. http_requests_total is a metric vector partitioned by HTTP method
// (label name "method") and HTTP status code (label name "code").
// * Logs requests and responses.
// * Adds spans and propagate trace metadata from request if any.
func (ins *middleware) WrapHandler(handlerName string, handler http.Handler) http.HandlerFunc {
	reg := prometheus.WrapRegistererWith(prometheus.Labels{"handler": handlerName}, ins.reg)

	requestDuration := promauto.With(reg).NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Tracks the latencies for HTTP requests.",
			Buckets: ins.buckets,
		},
		[]string{"method", "code"},
	)
	requestSize := promauto.With(reg).NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "http_request_size_bytes",
			Help: "Tracks the size of HTTP requests.",
		},
		[]string{"method", "code"},
	)
	requestsTotal := promauto.With(reg).NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Tracks the number of HTTP requests.",
		}, []string{"method", "code"},
	)
	responseSize := promauto.With(reg).NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "http_response_size_bytes",
			Help: "Tracks the size of HTTP responses.",
		},
		[]string{"method", "code"},
	)
	// TODO(bwplotka): Add exemplars everywhere when supported: https://github.com/prometheus/client_golang/issues/854
	base := promhttp.InstrumentHandlerRequestSize(
		requestSize,
		promhttp.InstrumentHandlerCounter(
			requestsTotal,
			promhttp.InstrumentHandlerResponseSize(
				responseSize,
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					now := time.Now()

					wd := logging.WrapResponseWriterWithStatus(w)
					handler.ServeHTTP(wd, r)

					observer := requestDuration.WithLabelValues(strings.ToLower(r.Method), wd.Status())

					// SpanContext!
					if spanCtx := trace.SpanContextFromContext(r.Context()); spanCtx.HasTraceID() && spanCtx.IsSampled() {
						traceID := prometheus.Labels{"traceID": spanCtx.TraceID().String()}

						observer.(prometheus.ExemplarObserver).ObserveWithExemplar(time.Since(now).Seconds(), traceID)
						return
					}

					observer.Observe(time.Since(now).Seconds())
					return

				}),
			),
		),
	)

	if ins.logMiddleware != nil {
		// Add context values that gives more context to request logging.
		base = func(w http.ResponseWriter, r *http.Request) {
			spanCtx := tracing.GetSpan(r.Context()).Context()
			base.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), logging.RequestIDCtxKey, spanCtx.TraceID())))
		}
		base = ins.logMiddleware.WrapHandler(handlerName, base)
	}

	if ins.traceMiddleware != nil {
		// Wrap with tracing. This will be visited as a first middleware.
		base = ins.traceMiddleware.WrapHandler(handlerName, base)
	}
	return base.ServeHTTP
}
