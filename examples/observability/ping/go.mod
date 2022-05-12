module github.com/bwplotka/correlator/examples/observability/ping

go 1.17

require (
	github.com/bwplotka/tracing-go v0.0.0-20220503165347-ff06a00b8232
	github.com/efficientgo/tools/core v0.0.0-20220225185207-fe763185946b
	github.com/go-kit/kit v0.12.0
	github.com/oklog/run v1.1.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.12.1
	github.com/prometheus/common v0.32.1
	go.opentelemetry.io/otel v1.6.3
	go.opentelemetry.io/otel/trace v1.6.3
)

replace (
	github.com/bwplotka/tracing-go => ../../../../tracing-go
)
