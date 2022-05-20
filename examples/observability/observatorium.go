package observability

import (
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/bwplotka/correlator/pkg/correlator"
	"github.com/efficientgo/e2e"
	"github.com/ghodss/yaml"
	"github.com/pkg/errors"
	commoncfg "github.com/prometheus/common/config"
	"github.com/prometheus/prometheus/config"
	"github.com/thanos-io/thanos/pkg/httpconfig"
	"github.com/thanos-io/thanos/test/e2e/e2ethanos"
)

const backendName = "observatorium"

type Observatorium struct {
	receive e2e.Runnable
	loki    e2e.Runnable
	jaeger  e2e.Runnable

	querier e2e.Runnable
	grafana e2e.Runnable
}

// NOTE(bwplotka): All endpoints are internal, so they cannot be reachable from host (only from docker network).

func (o *Observatorium) MetricsWriteEndpoint() string {
	return e2ethanos.RemoteWriteEndpoint(o.receive.InternalEndpoint("remote-write"))
}

func (o *Observatorium) LogsWriteEndpoint() string {
	return "http://" + o.loki.InternalEndpoint("http") + "/loki/api/v1/push"
}

func (o *Observatorium) TracesWriteEndpoint() string {
	// TODO(bwplotka): Ideally it's OTLP, but Jaeger does not implement it yet (https://github.com/jaegertracing/jaeger/issues/3625).
	return o.jaeger.InternalEndpoint("jaeger.thrift-model.proto")
}

func (o *Observatorium) ProfilesWriteEndpoint() string {
	return "TODO"
}

// startObservatorium starts Observatorium (http://observatorium.io/) like simplified setup to mimic multi-signal backend.
func startObservatorium(env e2e.Environment) (*Observatorium, error) {
	o := &Observatorium{}

	// Start Thanos for metrics.
	// Simplified stack - no compaction, no object storage, just filesystem and inmem WAL, plus ruling/alerting.
	ruleFuture := e2ethanos.NewRulerBuilder(env, backendName).
		WithImage("quay.io/thanos/thanos:v0.26.0")
	o.receive = e2ethanos.NewReceiveBuilder(env, backendName).
		WithExemplarsInMemStorage(1e6).
		WithImage("quay.io/thanos/thanos:v0.26.0").
		Init()
	o.querier = e2ethanos.NewQuerierBuilder(env, backendName).
		WithStoreAddresses(o.receive.InternalEndpoint("grpc")).
		WithRuleAddresses(ruleFuture.InternalEndpoint("grpc")).
		WithExemplarAddresses(o.receive.InternalEndpoint("grpc")).
		WithImage("quay.io/thanos/thanos:v0.26.0").
		Init()

	u, err := url.Parse(e2ethanos.RemoteWriteEndpoint(o.receive.InternalEndpoint("remote-write")))
	if err != nil {
		return nil, err
	}

	const pingHTTPErrorsAlert = `
groups:
- name: ping-service-alerts
  interval: 5s
  rules:
  - alert: PingService_TooManyErrors
    expr: sum(rate(http_requests_total{handler="/ping",code!~"2.."}[1m])) by (job, instance) / sum(rate(http_requests_total{handler="/ping"}[1m])) by (job, instance) > 0.3
    labels:
      severity: page
    annotations:
      summary: "To many ping errors!"
`
	if err := os.MkdirAll(filepath.Join(ruleFuture.Dir(), "rules"), os.ModePerm); err != nil {
		return nil, err
	}
	if err := ioutil.WriteFile(filepath.Join(ruleFuture.Dir(), "rules", "alert.yaml"), []byte(pingHTTPErrorsAlert), 0666); err != nil {
		return nil, err
	}

	rule := ruleFuture.InitStateless(filepath.Join(ruleFuture.InternalDir(), "rules"), []httpconfig.Config{
		{EndpointsConfig: httpconfig.EndpointsConfig{
			StaticAddresses: []string{o.querier.InternalEndpoint("http")},
			Scheme:          "http",
		}},
	}, []*config.RemoteWriteConfig{{URL: &commoncfg.URL{URL: u}}})

	// Loki + Grafana as Loki does not have it's own UI.
	o.loki = NewLoki(env, backendName)
	o.grafana = NewLokiGrafana(env, backendName, o.loki)

	// Jaeger for traces.
	o.jaeger = NewJaeger(env, backendName)

	if err := e2e.StartAndWaitReady(o.receive, o.querier, o.loki, o.grafana, o.jaeger, rule); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *Observatorium) StartCorrelator(env e2e.Environment, name string, parca e2e.Runnable) e2e.Runnable {
	f := e2e.NewInstrumentedRunnable(env, fmt.Sprintf("correlator-%s", name)).WithPorts(map[string]int{"http": 8080}, "http").Future()

	c := correlator.Config{
		Sources: correlator.Sources{
			Thanos: correlator.ThanosSource{
				Source: correlator.Source{
					InternalEndpoint: o.querier.Endpoint("http"), // o.querier.InternalEndpoint("http"),
					ExternalEndpoint: o.querier.Endpoint("http"),
				},
			},
			Loki: correlator.LokiSource{
				Source: correlator.Source{
					InternalEndpoint: o.loki.Endpoint("http"), // o.loki.InternalEndpoint("http"),
					ExternalEndpoint: o.loki.Endpoint("http"),
				},
				UISource: correlator.Source{
					InternalEndpoint: o.grafana.Endpoint("http"),
					ExternalEndpoint: o.grafana.Endpoint("http"),
				},
			},
			Jaeger: correlator.JaegerSource{
				Source: correlator.Source{
					InternalEndpoint: o.jaeger.Endpoint("http"), // o.jaeger.InternalEndpoint("http"),
					ExternalEndpoint: o.jaeger.Endpoint("http"),
				},
			},
			Parca: correlator.ParcaSource{
				Source: correlator.Source{
					InternalEndpoint: parca.Endpoint("http"), // o.parca.InternalEndpoint("http"),
					ExternalEndpoint: parca.Endpoint("http"),
				},
			},
		},
	}
	b, err := yaml.Marshal(&c)
	if err != nil {
		return e2e.NewErrInstrumentedRunnable(name, err)
	}
	// For debug only.
	if err := os.WriteFile(filepath.Join("../../config.yaml"), b, os.ModePerm); err != nil {
		return e2e.NewErrInstrumentedRunnable(name, err)
	}
	if err := os.WriteFile(filepath.Join(f.Dir(), "config.yaml"), b, os.ModePerm); err != nil {
		return e2e.NewErrInstrumentedRunnable(name, err)
	}

	return f.Init(e2e.StartOptions{
		Image:     "correlator:latest",
		Command:   e2e.NewCommand("/correlator", "--config-file="+filepath.Join(f.InternalDir(), "config.yaml")),
		User:      strconv.Itoa(os.Getuid()),
		Readiness: e2e.NewTCPReadinessProbe("http"),
	})
}

// NewLokiGrafana was blamelessly copied (and adjusted) from Ian's demo, thanks to the fact we all use e2e framework.
// https://github.com/bill3tt/warp-speed-debugging-demo/blob/66f51e1f6d87cfc8cc6804465844ca8da6f22bea/test/interactive_test.go
func NewLokiGrafana(env e2e.Environment, name string, logBackend e2e.Linkable) e2e.InstrumentedRunnable {
	f := e2e.NewInstrumentedRunnable(env, fmt.Sprintf("grafana-%s", name)).WithPorts(map[string]int{"http": 3000}, "http").Future()

	// DO NOT USE this configuration file in any non-example setting.
	// It disabled authentication and gives anonymous users admin access to this Grafana instance.
	config := `
[auth.anonymous]
enabled = true
org_name = Main Org.
org_role = Admin
[security]
cookie_samesite = none
[feature_toggles]
enable = tempoSearch tempoBackendSearch
[log]
level = error
`
	if err := ioutil.WriteFile(filepath.Join(f.Dir(), "grafana.ini"), []byte(config), 0600); err != nil {
		return e2e.NewErrInstrumentedRunnable(name, errors.Wrap(err, "create grafana config failed"))
	}

	datasources := fmt.Sprintf(`
apiVersion: 1
datasources:
  - name: Logging
    uid: loki
    url: %s
    type: loki
`, logBackend.InternalEndpoint("http"))
	if err := os.MkdirAll(filepath.Join(f.Dir(), "datasources"), os.ModePerm); err != nil {
		return e2e.NewErrInstrumentedRunnable(name, errors.Wrap(err, "create grafana datasources dir failed"))
	}
	if err := ioutil.WriteFile(filepath.Join(f.Dir(), "datasources", "datasources.yaml"), []byte(datasources), os.ModePerm); err != nil {
		return e2e.NewErrInstrumentedRunnable(name, errors.Wrap(err, "create grafana datasources failed"))
	}

	return f.Init(e2e.StartOptions{
		Image: "grafana/grafana:8.3.2",
		User:  strconv.Itoa(os.Getuid()),
		EnvVars: map[string]string{
			"GF_PATHS_CONFIG":       filepath.Join(f.InternalDir(), "grafana.ini"),
			"GF_PATHS_PROVISIONING": f.InternalDir(),
		},
		Readiness: e2e.NewHTTPReadinessProbe("http", "/", 200, 200),
	})
}

// NewLoki was blamelessly copied (and adjusted) from Ian's demo, thanks to the fact we all use e2e framework.
// https://github.com/bill3tt/warp-speed-debugging-demo/blob/66f51e1f6d87cfc8cc6804465844ca8da6f22bea/test/interactive_test.go#L199
func NewLoki(env e2e.Environment, name string) e2e.InstrumentedRunnable {
	f := e2e.NewInstrumentedRunnable(env, fmt.Sprintf("loki-%v", name)).WithPorts(map[string]int{"http": 3100}, "http").Future()

	config := `
auth_enabled: false
server:
  http_listen_port: 3100
ingester:
  lifecycler:
    address: 127.0.0.1
    ring:
      kvstore:
        store: inmemory
      replication_factor: 1
    final_sleep: 0s
  chunk_idle_period: 5m
  chunk_retain_period: 30s
schema_config:
  configs:
  - from: 2018-04-15
    store: boltdb
    object_store: filesystem
    schema: v9
    index:
      prefix: index_
      period: 168h
storage_config:
  boltdb:
    directory: /tmp/loki/index
  filesystem:
    directory: /tmp/loki/chunks
limits_config:
  enforce_metric_name: false
  reject_old_samples: true
  reject_old_samples_max_age: 168h
  ingestion_rate_mb: 40 # We surpassed 4MB just with 2 app logging on one laptop?
chunk_store_config:
  max_look_back_period: 0
table_manager:
  chunk_tables_provisioning:
    inactive_read_throughput: 0
    inactive_write_throughput: 0
    provisioned_read_throughput: 0
    provisioned_write_throughput: 0
  index_tables_provisioning:
    inactive_read_throughput: 0
    inactive_write_throughput: 0
    provisioned_read_throughput: 0
    provisioned_write_throughput: 0
  retention_deletes_enabled: false
  retention_period: 0
`

	if err := ioutil.WriteFile(filepath.Join(f.Dir(), "loki.yaml"), []byte(config), os.ModePerm); err != nil {
		return e2e.NewErrInstrumentedRunnable(name, errors.Wrap(err, "create loki config failed"))
	}

	args := e2e.BuildArgs(map[string]string{
		"-config.file":      filepath.Join(f.InternalDir(), "loki.yaml"),
		"-ingester.wal-dir": f.InternalDir(),
	})

	return f.Init(
		e2e.StartOptions{
			Image:     "grafana/loki:2.5.0",
			User:      strconv.Itoa(os.Getuid()),
			Command:   e2e.NewCommandWithoutEntrypoint("loki", args...),
			Readiness: e2e.NewHTTPReadinessProbe("http", "/ready", 200, 200),
		},
	)
}

func NewJaeger(env e2e.Environment, name string) e2e.InstrumentedRunnable {
	return e2e.NewInstrumentedRunnable(env, fmt.Sprintf("jaeger-%s", name)).
		WithPorts(
			map[string]int{
				"http":                      16686,
				"http.admin":                14269,
				"jaeger.thrift-model.proto": 14250, //	 gRPC	used by jaeger-agent to send spans in model.proto format
			}, "http.admin").
		Init(e2e.StartOptions{
			Image:     "jaegertracing/all-in-one:1.33",
			Readiness: e2e.NewHTTPReadinessProbe("http.admin", "/", 200, 200),
		})
}

func NewParca(env e2e.Environment, name string, targets ...e2e.Runnable) e2e.InstrumentedRunnable {
	f := e2e.NewInstrumentedRunnable(env, fmt.Sprintf("parca-%s", name)).WithPorts(map[string]int{"http": 7070}, "http").Future()

	config := `
debug_info:
  bucket:
    type: "FILESYSTEM"
    config:
      directory: "./tmp"
  cache:
    type: "FILESYSTEM"
    config:
      directory: "./tmp"

scrape_configs:
#  - job_name: "default"
#    scrape_interval: "3s"
#    static_configs:
#      - targets: [ '127.0.0.1:7070' ]
`
	for _, t := range targets {
		config = config + fmt.Sprintf(`
  - job_name: "%s"
    scrape_interval: "3s"
    static_configs:
      - targets: [ '%s' ]
`, t.InternalEndpoint("http"), t.InternalEndpoint("http"))
	}

	if err := ioutil.WriteFile(filepath.Join(f.Dir(), "parca.yaml"), []byte(config), 0600); err != nil {
		return e2e.NewErrInstrumentedRunnable(name, errors.Wrap(err, "create parca config failed"))
	}

	// profile_label_trace_id="0d89ae4c473862caa8d0e79cbdfc13e4"
	return f.Init(e2e.StartOptions{
		Image:     "ghcr.io/parca-dev/parca:pprof-label-query",
		Command:   e2e.NewCommand("/parca", "--config-path="+filepath.Join(f.InternalDir(), "parca.yaml")),
		User:      strconv.Itoa(os.Getuid()),
		Readiness: e2e.NewTCPReadinessProbe("http"),
	})
}
