package observability

import (
	"testing"

	"github.com/efficientgo/e2e"
	e2einteractive "github.com/efficientgo/e2e/interactive"
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
	envObs, err := e2e.NewDockerEnvironment("e2e_correlator_observatorium")
	testutil.Ok(t, err)
	t.Cleanup(envObs.Close)

	o, err := startObservatorium(envObs)
	testutil.Ok(t, err)

	//// Create remote docker environment to simulate remote setup!
	//// TODO(bwplotka): Can container talk to another container in another network through localhost? We shall see..
	//envClient, err := e2e.NewDockerEnvironment("e2e_correlator_client")
	//testutil.Ok(t, err)
	//t.Cleanup(envClient.Close)

	testutil.Ok(t, e2einteractive.OpenInBrowser(o.GrafanaUI()))
	testutil.Ok(t, e2einteractive.RunUntilEndpointHit())
}
