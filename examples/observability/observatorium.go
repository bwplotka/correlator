package observability

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"

	"github.com/efficientgo/e2e"
	"github.com/pkg/errors"
	"github.com/thanos-io/thanos/test/e2e/e2ethanos"
)

const backendName = "observatorium"

type Observatorium struct {
	receive e2e.Runnable
	loki    e2e.Runnable
	tempo   e2e.Runnable

	querier e2e.Runnable
	grafana e2e.Runnable
}

// NOTE(bwplotka): All endpoints are external, so they can be reachable from different docker network (yolo).

func (o *Observatorium) MetricsWriteEndpoint() string {
	return e2ethanos.RemoteWriteEndpoint(o.receive.Endpoint("remote-write"))
}

func (o *Observatorium) LogsWriteEndpoint() string {
	return o.loki.Endpoint("http")
}

func (o *Observatorium) TracesWriteEndpoint() string {
	return o.tempo.Endpoint("otlp")
}

func (o *Observatorium) GrafanaUI() string {
	return "http://" + o.grafana.Endpoint("http")
}

// startObservatorium starts Observatorium (http://observatorium.io/) like simplified setup to mimic multi-signal backend.
func startObservatorium(env e2e.Environment) (*Observatorium, error) {
	o := &Observatorium{}

	// Start Thanos for metrics.
	// Simplified stack - no compaction, no object storage, just filesystem and inmem WAL.
	o.receive = e2ethanos.NewReceiveBuilder(env, backendName).WithExemplarsInMemStorage(1e6).Init()
	o.querier = e2ethanos.NewQuerierBuilder(env, backendName).
		WithStoreAddresses(o.receive.InternalEndpoint("grpc")).
		WithExemplarAddresses(o.receive.InternalEndpoint("grpc")).
		Init()
	o.loki = NewLoki(env, backendName)
	o.tempo = NewTempo(env, backendName)
	o.grafana = NewGrafana(env, backendName, o.querier, o.loki, o.tempo)

	return o, e2e.StartAndWaitReady(o.receive, o.querier, o.loki, o.tempo, o.grafana)
}

// NewGrafana was blamelessly copied (and adjusted) from Ian's demo, thanks to the fact we all use e2e framework.
// https://github.com/bill3tt/warp-speed-debugging-demo/blob/66f51e1f6d87cfc8cc6804465844ca8da6f22bea/test/interactive_test.go
func NewGrafana(env e2e.Environment, name string, metricBackend e2e.Linkable, logBackend e2e.Linkable, traceBackend e2e.Linkable) e2e.InstrumentedRunnable {
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
enable = tempoSearch tempoBackendSearch`
	if err := ioutil.WriteFile(filepath.Join(f.Dir(), "grafana.ini"), []byte(config), 0600); err != nil {
		return e2e.NewErrInstrumentedRunnable(name, errors.Wrap(err, "create grafana config failed"))
	}

	datasources := fmt.Sprintf(`
apiVersion: 1
datasources:
  - name: Metrics
    uid: thanos
    url: %s
    type: prometheus
    jsonData:
      httpMethod: POST
      exemplarTraceIdDestinations:
        - datasourceUid: tempo
          name: traceId
        - datasourceUid: loki
          name: traceId
          url: 
  - name: Logging
    uid: loki
    url: %s
    type: loki
  - name: Tracing
    uid: tempo
    url: %s
    type: tempo`, metricBackend.InternalEndpoint("http"), logBackend.InternalEndpoint("http"), traceBackend.InternalEndpoint("http"))
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
			Image:   "grafana/loki:2.5.0",
			User:    strconv.Itoa(os.Getuid()),
			Command: e2e.NewCommandWithoutEntrypoint("loki", args...),
			Volumes: []string{f.Dir()},
		},
	)
}

// NewTempo was blamelessly copied (and adjusted) from Ian's demo, thanks to the fact we all use e2e framework.
// https://github.com/bill3tt/warp-speed-debugging-demo/blob/66f51e1f6d87cfc8cc6804465844ca8da6f22bea/test/interactive_test.go#L199
func NewTempo(env e2e.Environment, name string) e2e.InstrumentedRunnable {
	config := `
server:
  http_listen_port: 3200
distributor:
  receivers:
    otlp:
      protocols:
        grpc:
ingester:
  trace_idle_period: 10s               # the length of time after a trace has not received spans to consider it complete and flush it
  max_block_bytes: 1_000_000           # cut the head block when it hits this size or ...
  max_block_duration: 5m               #   this much time passes
compactor:
  compaction:
    compaction_window: 1h              # blocks in this time window will be compacted together
    max_block_bytes: 100_000_000       # maximum size of compacted blocks
    block_retention: 1h
    compacted_block_retention: 10m
storage:
  trace:
    backend: local                     # backend configuration to use
    block:
      bloom_filter_false_positive: .05 # bloom filter false positive rate.  lower values create larger filters but fewer false positives
      index_downsample_bytes: 1000     # number of bytes per index record
      encoding: zstd                   # block encoding/compression.  options: none, gzip, lz4-64k, lz4-256k, lz4-1M, lz4, snappy, zstd, s2
    wal:
      path: /tmp/tempo/wal             # where to store the the wal locally
      encoding: snappy                 # wal encoding/compression.  options: none, gzip, lz4-64k, lz4-256k, lz4-1M, lz4, snappy, zstd, s2
    local:
      path: /tmp/tempo/blocks
    pool:
      max_workers: 100                 # worker pool determines the number of parallel requests to the object store backend
      queue_depth: 10000
search_enabled: true
`
	f := e2e.NewInstrumentedRunnable(env, fmt.Sprintf("tempo-%s", name)).WithPorts(map[string]int{
		"http":      3200,
		"oltp-grpc": 4317,
	}, "http").Future()

	if err := ioutil.WriteFile(filepath.Join(f.Dir(), "tempo.yaml"), []byte(config), os.ModePerm); err != nil {
		return e2e.NewErrInstrumentedRunnable(name, errors.Wrap(err, "create tempo config failed"))
	}

	args := e2e.BuildArgs(map[string]string{
		"-config.file": filepath.Join(f.InternalDir(), "tempo.yaml"),
	})

	return f.Init(
		e2e.StartOptions{
			Image:   "grafana/tempo:1.4.1",
			User:    strconv.Itoa(os.Getuid()),
			Command: e2e.NewCommandWithoutEntrypoint("/tempo", args...),
			Volumes: []string{f.Dir()},
		},
	)
}
