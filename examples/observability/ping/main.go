package main

import (
	"context"
	"flag"
	"fmt"
	stdlog "log"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bwplotka/correlator/examples/observability/ping/pkg/httpinstrumentation"
	"github.com/bwplotka/correlator/examples/observability/ping/pkg/logging"
	"github.com/bwplotka/tracing-go/tracing"
	"github.com/bwplotka/tracing-go/tracing/exporters/otlp"
	"github.com/efficientgo/tools/core/pkg/errcapture"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/oklog/run"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
)

var (
	// TODO(bwplotka): Move those flags out of globals.
	addr               = flag.String("listen-address", ":8080", "The address to listen on for HTTP requests.")
	appVersion         = flag.String("set-version", "first", "Injected version to be presented via metrics.")
	lat                = flag.String("latency", "90%500ms,10%200ms", "Encoded latency and probability of the response in format as: <probability>%<duration>,<probability>%<duration>....")
	successProb        = flag.Float64("success-prob", 100, "The probability (in %) of getting a successful response")
	traceEndpoint      = flag.String("trace-endpoint", "stdout", "The gRPC OTLP endpoint for tracing backend. Hack: Set it to 'stdout' to print traces to the output instead")
	traceSamplingRatio = flag.Float64("trace-sampling-ratio", 1.0, "Sampling ratio. Currently 1.0 is best value if you wish to use exemplars.")
	logLevel           = flag.String("log-level", "info", "Log filtering level. Possible values: \"error\", \"warn\", \"info\", \"debug\"")
	logFormat          = flag.String("log-format", logging.LogFormatLogfmt, fmt.Sprintf("Log format to use. Possible options: %s or %s", logging.LogFormatLogfmt, logging.LogFormatJSON))
	logFile            = flag.String("log-file", "", "File for logs.")
)

type latencyDecider struct {
	latencies     []time.Duration
	probabilities []float64 // Sorted ascending.
}

func newLatencyDecider(encodedLatencies string) (*latencyDecider, error) {
	l := latencyDecider{}

	s := strings.Split(encodedLatencies, ",")
	// Be smart, sort while those are encoded, so they are sorted by probability number.
	sort.Strings(s)

	cumulativeProb := 0.0
	for _, e := range s {
		entry := strings.Split(e, "%")
		if len(entry) != 2 {
			return nil, errors.Errorf("invalid input %v", encodedLatencies)
		}
		f, err := strconv.ParseFloat(entry[0], 64)
		if err != nil {
			return nil, errors.Wrapf(err, "parse probabilty %v as float", entry[0])
		}
		cumulativeProb += f
		l.probabilities = append(l.probabilities, f)

		d, err := time.ParseDuration(entry[1])
		if err != nil {
			return nil, errors.Wrapf(err, "parse latency %v as duration", entry[1])
		}
		l.latencies = append(l.latencies, d)
	}
	if cumulativeProb != 100 {
		return nil, errors.Errorf("overall probability has to equal 100. Parsed input equals to %v", cumulativeProb)
	}
	fmt.Println("Latency decider created:", l)
	return &l, nil
}

func (l latencyDecider) AddLatency(ctx context.Context, logger log.Logger) {
	_, span := tracing.StartSpan(ctx, "addingLatencyBasedOnProbability")
	defer span.End(nil)

	n := rand.Float64() * 100
	span.SetAttributes("latencyProbabilities", l.probabilities, "lucky%", n)

	for i, p := range l.probabilities {
		if n <= p {
			span.SetAttributes("latencyIntroduced", l.latencies[i].String())
			level.Debug(logger).Log("msg", "adding latency based on probability", "latencyIntroduced", l.latencies[i].String(), "latencyProbabilities", l.probabilities, "lucky%", n)
			<-time.After(l.latencies[i])
			return
		}
	}
}

func pingHandler(logger log.Logger, latDecider *latencyDecider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		latDecider.AddLatency(r.Context(), logger)

		if err := tracing.DoInSpan(r.Context(), "evaluatePing", func(ctx context.Context) error {
			tracing.GetSpan(ctx).SetAttributes("successProbability", *successProb)
			level.Debug(logger).Log("msg", "evalutating ping", "successProbability", *successProb)

			if rand.Float64()*100 <= *successProb {
				return nil
			}
			return errors.New("decided to NOT return success, sorry")
		}); err != nil {
			w.WriteHeader(http.StatusTeapot)
			// Not smart to pass error straight away. Sanitize on production.
			_, _ = fmt.Fprintln(w, err.Error())
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "pong")
	}
}

func main() {
	flag.Parse()
	if err := runMain(); err != nil {
		// Use %+v for github.com/pkg/errors error to print with stack.
		stdlog.Fatalf("Error: %+v", errors.Wrapf(err, "%s", flag.Arg(0)))
	}
}

func runMain() (err error) {
	latDecider, err := newLatencyDecider(*lat)
	if err != nil {
		return err
	}

	version.Version = *appVersion

	// Setup instrumentation: Prometheus registry for metrics, logger and tracer.
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		version.NewCollector("ping"),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	w := os.Stderr
	if *logFile != "" {
		f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_RDWR|os.O_APPEND, os.ModePerm)
		if err != nil {
			return err
		}
		defer errcapture.Do(&err, f.Close, "close log file")
		w = f
	}
	logger := logging.NewLogger(*logLevel, *logFormat, "ping", w)

	var exporter tracing.ExporterBuilder
	switch *traceEndpoint {
	case "stdout":
		exporter = tracing.NewWriterExporter(os.Stdout)
	default:
		exporter = otlp.Exporter(*traceEndpoint, otlp.WithInsecure())
	}

	tracer, closeFn, err := tracing.NewTracer(
		exporter,
		tracing.WithSampler(tracing.TraceIDRatioBasedSampler(*traceSamplingRatio)),
		tracing.WithServiceName("demo:ping"),
	)
	if err != nil {
		return err
	}
	defer errcapture.Do(&err, closeFn, "close tracers")

	level.Info(logger).Log("msg", "metrics, logs and tracing enabled", "logFile", *logFile, "traceEndpoint", *traceEndpoint)

	m := http.NewServeMux()
	m.Handle("/metrics", httpinstrumentation.NewMiddleware(reg, nil, logger, tracer).
		WrapHandler("/metrics", promhttp.HandlerFor(
			reg,
			promhttp.HandlerOpts{
				// Opt into OpenMetrics to support exemplars.
				EnableOpenMetrics: true,
			},
		)))
	m.HandleFunc("/ping", httpinstrumentation.NewMiddleware(reg, nil, logger, tracer).
		WrapHandler("/ping", pingHandler(logger, latDecider)))

	srv := http.Server{Addr: *addr, Handler: m}

	g := &run.Group{}
	g.Add(func() error {
		level.Info(logger).Log("msg", "starting HTTP server", "addr", *addr)
		if err := srv.ListenAndServe(); err != nil {
			return errors.Wrap(err, "starting web server")
		}
		return nil
	}, func(error) {
		if err := srv.Close(); err != nil {
			level.Error(logger).Log("msg", "failed to stop web server", "err", err)
		}
	})
	g.Add(run.SignalHandler(context.Background(), syscall.SIGINT, syscall.SIGTERM))
	return g.Run()
}
