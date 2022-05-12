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

// TestCorrelatorWithObservability is demo-ing the correlation example in the interactive test using standard go test with https://github.com/efficientgo/e2e framework.
// Scenario flow:
// * Starting Observatorium (like) Saas centric setup with: Thanos (IngesterReceive and Querier), Loki (all-in binary), Tempo (all-in in-mem) and Parca (TBD) with stateless Grafana.
// * Starting remote (like) observability client setup with Grafana Agent and Parca Agent (TBH).
// * Starting ping AND pinger app that are running in client environment, remote writing data to Observatorium setup. We will use that as observed workload.
//
// Now with this we will run "correlator" service in Observatorium that will hook into Grafana links and present a simple JSON result that allows navigating to different views and UIs.
func TestCorrelatorWithObservability(t *testing.T) {
	envObs, err := e2e.NewDockerEnvironment("e2e_correlator_observatorium")
	testutil.Ok(t, err)
	t.Cleanup(envObs.Close)

	// NOTE: You need thanos:latest image for this to work (run `make docker` on Thanos repo).
	o, err := startObservatorium(envObs)
	testutil.Ok(t, err)

	// Create remote docker environment to simulate remote setup!
	// TODO(bwplotka): Can container talk to another container in another network through localhost? We shall see..
	envClient, err := e2e.NewDockerEnvironment("e2e_correlator_client")
	testutil.Ok(t, err)
	t.Cleanup(envClient.Close)

	ping := e2e.NewInstrumentedRunnable(envClient, "ping").
		WithPorts(map[string]int{
			"http": 8080,
		}, "http").Init(e2e.StartOptions{
		Image: "ping:latest",
		User:  strconv.Itoa(os.Getuid()),
		//Command:   e2e.NewCommandWithoutEntrypoint("agent", append([]string{"-config.enable-read-api"}, args...)...),
		Readiness: e2e.NewHTTPReadinessProbe("http", "/ready", 200, 200),
	})

	NewGrafanaAgent(envClient, "eu-valencia1", o, )
	testutil.Ok(t, e2einteractive.OpenInBrowser("http://"+o.grafana.Endpoint("http")))
	testutil.Ok(t, e2einteractive.RunUntilEndpointHit())
}

func NewGrafanaAgent(env e2e.Environment, name string, obs *Observatorium, observables ...e2e.InstrumentedRunnable) e2e.InstrumentedRunnable {
	f := e2e.NewInstrumentedRunnable(env, fmt.Sprintf("grafana-agent-%v", name)).WithPorts(map[string]int{"http": 3100}, "http").Future()

	var metricScrapeJobs []string
	var logsScrapeJob []string

	for _, observable := range observables {
		metricScrapeJobs = append(metricScrapeJobs, fmt.Sprintf(`
    - job_name: %s
      static_configs:
      - targets: ['%s']`,
			observable.Name(),
			observable.InternalEndpoint("http"), // Hack, find typed way to find correct metric endpoint.
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
			filepath.Join(observable.InternalDir(), "out.log"), // Another hack.
		))
	}

	// TODO(bwplotka): Can we have that tail tracing solution? (:
	config := fmt.Sprintf(`
server:
  log_level: info

metrics:
  global:
    scrape_interval: 15s
    remote_write:
    - url: %s
      insecure: true
  configs:
  - name: default
    scrape_configs:
%s

logs:
  configs:
  - name: default
    clients:
      - url: %s
        insecure: true
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
        protocol: http
        format: jaeger
        insecure: true
    receivers:
      otlp:
	    protocols:
	      grpc:

integrations:
  metrics:
    autoscrape:
      enable: true
      metrics_instance: default

  # agent
  agent:
    extra_labels:
      cluster: "eu-valencia1"
`,
		obs.MetricsWriteEndpoint(),
		strings.Join(metricScrapeJobs, "\n"),
		obs.LogsWriteEndpoint(),
		strings.Join(logsScrapeJob, "\n"),
		obs.TracesWriteEndpoint(),
	)

	if err := ioutil.WriteFile(filepath.Join(f.Dir(), "agent.yaml"), []byte(config), os.ModePerm); err != nil {
		return e2e.NewErrInstrumentedRunnable(name, errors.Wrap(err, "create agent config failed"))
	}

	args := e2e.BuildArgs(map[string]string{
		"-config.file": filepath.Join(f.InternalDir(), "agent.yaml"),
	})

	return f.Init(
		e2e.StartOptions{
			Image:     "grafana/agent:v0.24.2",
			User:      strconv.Itoa(os.Getuid()),
			Command:   e2e.NewCommandWithoutEntrypoint("agent", append([]string{"-config.enable-read-api"}, args...)...),
			Readiness: e2e.NewHTTPReadinessProbe("http", "/ready", 200, 200),
		},
	)
}
