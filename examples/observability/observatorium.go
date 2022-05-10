package observability

import (
	"github.com/efficientgo/e2e"
	"github.com/thanos-io/thanos/test/e2e/e2ethanos"
)

type Observatorium struct {
}

// startObservatorium starts Observatorium (http://observatorium.io/) like simplified setup to mimic multi-signal backend.
func startObservatorium(env e2e.Environment) (Observatorium, error) {
	e2ethanos.NewQuerierBuilder()
}
