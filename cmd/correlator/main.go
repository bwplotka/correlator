package main

import (
	"context"
	"encoding/json"
	"flag"
	"html/template"
	stdlog "log"
	"net/http"
	"os"
	"syscall"

	"github.com/bwplotka/correlator/pkg/correlator"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/oklog/run"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/version"
)

const correlatorVersion = "v0.1.0"

const html = `
<html>
    <head>
    <title>Hello KubeConEU!</title>
    </head>
    <body>
        <form action="/correlate" method="post">
            Alert Firing? Tell me the Alert Name! <input type="alertname" name="alertname">
			</br>
			Wanna us to use Exemplars too? <input type="checkbox" name="useExemplar">
            <input type="submit" value="Correlate">
        </form>
    </body>
</html>
`

var (
	addr       = flag.String("listen-address", ":8080", "The address to listen on for HTTP requests.")
	configFile = flag.String("config-file", "", "Configuration file.")
	config     = flag.String("config", "", "YAML content for the configuration file.")
)

func main() {
	flag.Parse()
	if err := runMain(); err != nil {
		// Use %+v for github.com/pkg/errors error to print with stack.
		stdlog.Fatalf("Error: %+v", errors.Wrapf(err, "%s", flag.Arg(0)))
	}
}

func httpErrHandle(w http.ResponseWriter, code int, err error) {
	w.WriteHeader(code)
	_, _ = w.Write([]byte("{ \"error\": \" " + err.Error() + "\"}"))
}

func runMain() (err error) {
	version.Version = correlatorVersion

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		version.NewCollector("correlator"),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	logger := log.NewLogfmtLogger(os.Stderr)

	if *config != "" && *configFile != "" {
		return errors.New("can't set both -config and -config-file!")
	}

	var cfg correlator.Config
	if *config != "" {
		cfg, err = correlator.ParseConfig([]byte(*config))
		if err != nil {
			return errors.Wrap(err, "parse config")
		}
	} else if *configFile != "" {
		cfg, err = correlator.ParseConfigFromFile(*configFile)
		if err != nil {
			return errors.Wrap(err, "parse config from file")
		}
	} else {
		return errors.New("Set -config or -config-file!")
	}

	c, err := correlator.New(cfg, logger)
	if err != nil {
		return errors.Wrap(err, "new correlator")
	}

	m := http.NewServeMux()
	m.Handle("/metrics", promhttp.HandlerFor(
		reg,
		promhttp.HandlerOpts{
			// Opt into OpenMetrics to support exemplars.
			EnableOpenMetrics: true,
		},
	))

	t, err := template.New("login.gtpl").Parse(html)
	if err != nil {
		return err
	}

	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if err := t.Execute(w, nil); err != nil {
			httpErrHandle(w, 500, err)
		}
	})

	m.HandleFunc("/correlate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json; charset=utf-8")

		if err := r.ParseForm(); err != nil {
			httpErrHandle(w, http.StatusInternalServerError, err)
		}

		in := correlator.Input{IgnoreExemplar: true}
		
		useExemplar := r.Form["useExemplar"]
		if len(useExemplar) > 0 && useExemplar[0] == "on" {
			in.IgnoreExemplar = false
		}

		alertName := r.Form["alertname"]
		if len(alertName) == 0 {
			httpErrHandle(w, http.StatusBadRequest, errors.New("alertname parameter is required."))
			return
		}
		in.AlertName = alertName[0]
		discoveries, correlations, err := c.Correlate(r.Context(), in)
		if err != nil {
			httpErrHandle(w, http.StatusInternalServerError, err)
			return
		}

		b, err := json.Marshal(struct {
			Discoveries  []correlator.Discovery
			Correlations []correlator.Correlation
		}{
			Discoveries:  discoveries,
			Correlations: correlations,
		})
		if err != nil {
			httpErrHandle(w, http.StatusInternalServerError, err)
			return
		}

		w.WriteHeader(200)
		_, _ = w.Write(b)
	})

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
