package observability

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/efficientgo/e2e"
	e2einteractive "github.com/efficientgo/e2e/interactive"
	"github.com/efficientgo/tools/core/pkg/testutil"
	"github.com/pkg/errors"
)

const clientClusterName = "eu1-valencia"

// TestCorrelatorWithObservability is demo-ing the correlation example in the interactive test using standard go test with https://github.com/efficientgo/e2e framework.
// Scenario flow:
// * Starting Observatorium (like) Saas centric setup with: Thanos (IngesterReceive and Querier), Loki (all-in binary) + Grafana, Jaeger (all-in in-mem) and Parca (TBD).
// * Starting remote (like) observability client setup with Grafana Agent and Parca Agent (TBH).
// * Starting ping AND pinger app that are running in client environment, remote writing data to Observatorium setup. We will use that as observed workload.
//
// Now with this we will run "correlator" service in Observatorium that will hook into Grafana links and present a simple JSON result that allows navigating to different views and UIs.
// NOTE(bwplotka): Prerequsite is to run make docker from root of this repo.
func TestCorrelatorWithObservability(t *testing.T) {
	env, err := e2e.NewDockerEnvironment("e2e-correlation")
	testutil.Ok(t, err)
	t.Cleanup(env.Close)

	o, err := startObservatorium(env)
	testutil.Ok(t, err)

	// Client setup.
	var parca e2e.Runnable
	{
		agentFuture := NewGrafanaAgentFuture(env, clientClusterName)

		// Logs won't be visible to in test output - they are not directed to the file.
		ping := NewObservablePingService(env, clientClusterName, agentFuture.InternalEndpoint("grpc"))
		pinger := NewObservablePingerService(env, clientClusterName, ping, agentFuture.InternalEndpoint("grpc"))

		// Profiles.
		parca = NewParca(env, backendName, ping)

		agent := NewGrafanaAgent(agentFuture, o, ping, pinger)
		testutil.Ok(t, e2e.StartAndWaitReady(ping, pinger, agent, parca))

		_, err := o.StartCorrelator(env, parca)
		testutil.Ok(t, err)
	}

	testutil.Ok(t, e2einteractive.OpenInBrowser("http://"+o.querier.Endpoint("http")+"/alerts"))
	testutil.Ok(t, e2einteractive.OpenInBrowser("http://"+o.querier.Endpoint("http")+"/graph"))
	testutil.Ok(t, e2einteractive.OpenInBrowser("http://"+o.grafana.Endpoint("http")))
	testutil.Ok(t, e2einteractive.OpenInBrowser("http://"+o.jaeger.Endpoint("http")))
	testutil.Ok(t, e2einteractive.OpenInBrowser("http://"+parca.Endpoint("http")))
	testutil.Ok(t, e2einteractive.RunUntilEndpointHit())
}

type ObservableService interface {
	e2e.InstrumentedRunnable
	LogFile() string
	MetricPortName() string
}

type obsService struct {
	e2e.InstrumentedRunnable
	logFile        string
	metricPortName string
}

func (o *obsService) LogFile() string {
	return o.logFile
}

func (o *obsService) MetricPortName() string {
	return o.metricPortName
}

func NewObservablePingService(env e2e.Environment, name, traceEndpoint string) ObservableService {
	f := e2e.NewInstrumentedRunnable(env, fmt.Sprintf("ping-%s", name)).WithPorts(map[string]int{
		"http": 8080,
	}, "http").Future()

	o := &obsService{
		logFile:        filepath.Join(f.InternalDir(), "out.log"),
		metricPortName: "http",
	}

	o.InstrumentedRunnable = f.Init(e2e.StartOptions{
		Image: "ping:latest",
		User:  strconv.Itoa(os.Getuid()),
		Command: e2e.NewCommandWithoutEntrypoint("/bin/ping",
			"-set-version=v0.0.7",
			"-latency=90%300ms,10%200ms",
			"-success-prob=45",
			"-trace-endpoint="+traceEndpoint,
			"-log-file="+o.LogFile(),
			"-log-level=debug",
			"-log-format=json",
		),
		Readiness: e2e.NewHTTPReadinessProbe("http", "/metrics", 200, 200),
	})
	return o
}

func NewObservablePingerService(env e2e.Environment, name string, ping e2e.Runnable, traceEndpoint string) ObservableService {
	f := e2e.NewInstrumentedRunnable(env, fmt.Sprintf("pinger-%s", name)).WithPorts(map[string]int{
		"http": 8080,
	}, "http").Future()

	o := &obsService{
		logFile:        filepath.Join(f.InternalDir(), "out.log"),
		metricPortName: "http",
	}

	o.InstrumentedRunnable = f.Init(e2e.StartOptions{
		Image: "ping:latest",
		User:  strconv.Itoa(os.Getuid()),
		Command: e2e.NewCommandWithoutEntrypoint("/bin/pinger",
			"-endpoint=http://"+ping.InternalEndpoint("http")+"/ping",
			"-pings-per-second=1",
			"-trace-endpoint="+traceEndpoint,
			"-log-file="+o.LogFile(),
			"-log-level=debug",
			"-log-format=json",
		),
		Readiness: e2e.NewHTTPReadinessProbe("http", "/metrics", 200, 200),
	})
	return o
}

func NewGrafanaAgentFuture(env e2e.Environment, name string) e2e.FutureInstrumentedRunnable {
	return e2e.NewInstrumentedRunnable(env, fmt.Sprintf("grafana-agent-%v", name)).
		WithPorts(map[string]int{
			"http": 12345,
			"grpc": 4317,
		}, "http").Future()

}

func NewGrafanaAgent(f e2e.FutureInstrumentedRunnable, obs *Observatorium, observables ...ObservableService) e2e.InstrumentedRunnable {
	var metricScrapeJobs []string
	var logsScrapeJob []string

	for _, observable := range observables {
		metricScrapeJobs = append(metricScrapeJobs, fmt.Sprintf(`
    - job_name: %s
      static_configs:
      - targets: ['%s']`,
			observable.Name(),
			observable.InternalEndpoint(observable.MetricPortName()),
		))

		logsScrapeJob = append(logsScrapeJob, fmt.Sprintf(`
    - job_name: %s
      static_configs:
      - targets: [localhost]
        labels:
          jobs: %s
          __path__: %s`,
			observable.Name(),
			observable.Name(),
			observable.LogFile(),
		))
	}

	// TODO(bwplotka): Can we have that tail tracing solution? (:
	// https://grafana.com/docs/mimir/latest/operators-guide/using-exemplars/before-you-begin/ is useful!
	config := fmt.Sprintf(`
server:
  log_level: info

metrics:
  global:
    scrape_interval: 15s
    external_labels:
      cluster: eu1-valencia-laptop
    remote_write:
    - url: %s
      send_exemplars: true
  configs:
  - name: default
    scrape_configs:
%s

logs:
  configs:
  - name: default
    clients:
      - url: %s
    positions:
      filename: /tmp/positions.yaml
    scrape_configs:
%s

traces:
  configs:
  - name: default
    remote_write:
      - endpoint: %s
        insecure: true
        protocol: grpc # Agent does not support HTTP Jaeger format......
        format: jaeger
    receivers:
      otlp:
        protocols:
          grpc:
            endpoint: "0.0.0.0:4317" 
          http:
`,
		obs.MetricsWriteEndpoint(),
		strings.Join(metricScrapeJobs, "\n"),
		obs.LogsWriteEndpoint(),
		strings.Join(logsScrapeJob, "\n"),
		obs.TracesWriteEndpoint(),
	)

	if err := ioutil.WriteFile(filepath.Join(f.Dir(), "agent.yaml"), []byte(config), os.ModePerm); err != nil {
		return e2e.NewErrInstrumentedRunnable(f.Name(), errors.Wrap(err, "create agent config failed"))
	}

	args := e2e.BuildArgs(map[string]string{
		"-config.file":         filepath.Join(f.InternalDir(), "agent.yaml"),
		"-server.http.address": "0.0.0.0:12345",
		"-server.grpc.address": "0.0.0.0:12346",
	})

	return f.Init(
		e2e.StartOptions{
			Image:   "grafana/agent:v0.24.2",
			Command: e2e.NewCommandWithoutEntrypoint("agent", append([]string{"-config.enable-read-api"}, args...)...),
			//Readiness: e2e.NewHTTPReadinessProbe("http", "/", 200, 200),
		},
	)
}
