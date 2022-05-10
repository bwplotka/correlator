package main

import (
	"context"
	"flag"
	"fmt"
	stdlog "log"
	"net/http"
	"syscall"

	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/oklog/run"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
)

const correlatorVersion = "v0.1.0"

var (
	// TODO(bwplotka): Move those flags out of globals.
	addr = flag.String("listen-address", ":8080", "The address to listen on for HTTP requests.")
)

func handleCorrelate(w http.ResponseWriter, r *http.Request) {
	// TBD

	w.WriteHeader(http.StatusNotImplemented)
}

func main() {
	flag.Parse()
	if err := runMain(); err != nil {
		// Use %+v for github.com/pkg/errors error to print with stack.
		stdlog.Fatalf("Error: %+v", errors.Wrapf(err, "%s", flag.Arg(0)))
	}
}

func runMain() (err error) {
	version.Version = correlatorVersion

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		version.NewCollector("app"),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := http.NewServeMux()
	m.Handle("/metrics", promhttp.HandlerFor(
		reg,
		promhttp.HandlerOpts{
			// Opt into OpenMetrics to support exemplars.
			EnableOpenMetrics: true,
		},
	))
	m.HandleFunc("/api/v1/correlate", handleCorrelate)
	srv := http.Server{Addr: *addr, Handler: m}

	// Setup multiple 2 jobs. One is for serving HTTP requests, second to listen for Linux signals like Ctrl+C.
	g := &run.Group{}
	g.Add(func() error {
		fmt.Println("HTTP Server listening on", *addr)
		if err := srv.ListenAndServe(); err != nil {
			return errors.Wrap(err, "starting web server")
		}
		return nil
	}, func(error) {
		if err := srv.Close(); err != nil {
			fmt.Println("Failed to stop web server:", err)
		}
	})
	g.Add(run.SignalHandler(context.Background(), syscall.SIGINT, syscall.SIGTERM))
	return g.Run()
}
