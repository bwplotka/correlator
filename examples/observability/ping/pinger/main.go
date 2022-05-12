package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	stdlog "log"
	"net/http"
	"os"
	"sync"
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
	endpoint           = flag.String("endpoint", "http://app.demo.svc.cluster.local:8080/ping", "The address of pong app we can connect to and send requests.")
	pingsPerSec        = flag.Int("pings-per-second", 10, "How many pings per second we should request")
	traceEndpoint      = flag.String("trace-endpoint", "stdout", "The gRPC OTLP endpoint for tracing backend. Hack: Set it to 'stdout' to print traces to the output instead")
	traceSamplingRatio = flag.Float64("trace-sampling-ratio", 1.0, "Sampling ratio. Currently 1.0 is best value if you wish to use exemplars.")
	logLevel           = flag.String("log.level", "info", "Log filtering level. Possible values: \"error\", \"warn\", \"info\", \"debug\"")
	logFormat          = flag.String("log.format", logging.LogFormatLogfmt, fmt.Sprintf("Log format to use. Possible options: %s or %s", logging.LogFormatLogfmt, logging.LogFormatJSON))
)

func main() {
	flag.Parse()
	if err := runMain(); err != nil {
		// Use %+v for github.com/pkg/errors error to print with stack.
		stdlog.Fatalf("Error: %+v", errors.Wrapf(err, "%s", flag.Arg(0)))
	}
}

func runMain() (err error) {
	version.Version = "v0.0.7" // yolo.

	// Setup instrumentation: Prometheus registry for metrics, logger and tracer.
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		version.NewCollector("ping"),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	logger := logging.NewLogger(*logLevel, *logFormat, "ping")

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

	level.Info(logger).Log("msg", "metrics, logs and tracing enabled", "traceEndpoint", *traceEndpoint)

	m := http.NewServeMux()
	m.Handle("/metrics", httpinstrumentation.NewMiddleware(reg, nil, logger, tracer).
		WrapHandler("/metrics", promhttp.HandlerFor(
			reg,
			promhttp.HandlerOpts{
				// Opt into OpenMetrics to support exemplars.
				EnableOpenMetrics: true,
			},
		)))
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
	{
		client := &http.Client{
			// Custom HTTP client with metrics and tracing instrumentation.
			// TODO(bwplotka): Add tripperware.
			//Transport: exthttp.NewInstrumentationTripperware(reg, nil, tracingProvider).
			//	WrapRoundTripper("ping", http.DefaultTransport),
		}

		ctx, cancel := context.WithCancel(context.Background())
		g.Add(func() error {
			spamPings(ctx, client, logger, *endpoint, *pingsPerSec)
			return nil
		}, func(error) {
			cancel()
		})
	}
	g.Add(run.SignalHandler(context.Background(), syscall.SIGINT, syscall.SIGTERM))
	return g.Run()
}

func spamPings(ctx context.Context, client *http.Client, logger log.Logger, endpoint string, pingsPerSec int) {
	var wg sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-time.After(1 * time.Second):
		}

		for i := 0; i < pingsPerSec; i++ {
			wg.Add(1)
			go ping(ctx, client, logger, endpoint, &wg)
		}
	}
}

func ping(ctx context.Context, client *http.Client, logger log.Logger, endpoint string, wg *sync.WaitGroup) {
	defer wg.Done()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	r, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		level.Error(logger).Log("msg", "failed to create request", "err", err)
		return
	}
	res, err := client.Do(r)
	if err != nil {
		level.Error(logger).Log("msg", "failed to send request", "err", err)
		return
	}
	if res.StatusCode != http.StatusOK {
		level.Error(logger).Log("msg", "failed to send request", "code", res.StatusCode)
	}
	if res.Body != nil {
		// We don't care about response, just release resources.
		_, _ = io.Copy(ioutil.Discard, res.Body)
		_ = res.Body.Close()
	}
}
