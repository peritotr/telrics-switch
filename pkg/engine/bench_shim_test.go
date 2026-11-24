// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"context"
	"time"

	"softswitch/core/pkg/routing"
)

// deliverForBench calls deliver as the engine does for a call from the
// snapshot's first trunk.
func deliverForBench(e *Engine, ctx context.Context, snap *routing.Snapshot, d routing.Decision) ([]routing.Route, error) {
	routes, _, err := e.deliver(ctx, snap, d.Routes, snap.Trunks[0], time.Now())
	return routes, err
}
