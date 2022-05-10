package observability

import (
	"testing"

	"github.com/efficientgo/e2e"
	"github.com/efficientgo/tools/core/pkg/testutil"
)

// TestCorrelatorWithObservability is demo-ing the correlation example in the interactive test using standard go test with https://github.com/efficientgo/e2e framework.
// Scenario flow:
// * Starting Observatorium (like) Saas centric setup with: Thanos (IngesterReceive and Querier), Loki (all-in binary), Tempo (all-in in-mem) and Parca (TBD) with stateless Grafana.
// * Starting remote (like) observability client setup with Grafana Agent and Parca Agent (TBH).
// * Starting ping AND pinger app that are running in client environment, remote writing data to Observatorium setup. We will use that as observed workload.
//
// Now with this we will run "correlator" service in Observatorium that will hook into Grafana links and present a simple JSON result that allows navigating to different views and UIs.
func TestCorrelatorWithObservability(t *testing.T) {
	e, err := e2e.NewDockerEnvironment("e2e_correlator")
	testutil.Ok(t, err)
	t.Cleanup(e.Close)

	testutil.Ok(t, startObservatorium(e))

}
