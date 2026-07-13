package readiness

import (
	"context"
	"fmt"
	"time"

	"dkulpa.eu/game-server-snooze/pkg/server"
)

// PterodactylProbe treats the panel's running state as backend readiness. The
// Palworld egg emits its startup marker only after REST and the gameplay UDP
// listener are both ready, so this state is the cross-component readiness
// contract rather than an unsupported game query protocol.
type PterodactylProbe struct {
	controller   server.Controller
	pollInterval time.Duration
}

func NewPterodactylProbe(controller server.Controller, pollInterval time.Duration) (*PterodactylProbe, error) {
	if controller == nil || pollInterval <= 0 {
		return nil, fmt.Errorf("Pterodactyl controller and positive poll interval are required")
	}
	return &PterodactylProbe{controller: controller, pollInterval: pollInterval}, nil
}

func (probe *PterodactylProbe) Probe(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("probe Pterodactyl readiness: %w", err)
	}
	state, err := probe.controller.Status(ctx)
	if err != nil {
		return fmt.Errorf("probe Pterodactyl readiness: %w", err)
	}
	if state != server.StateRunning {
		return fmt.Errorf("Pterodactyl backend state is %q, not running", state)
	}
	return nil
}

func (probe *PterodactylProbe) WaitReady(ctx context.Context) error {
	for {
		err := probe.Probe(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("wait for Pterodactyl readiness: %w", ctx.Err())
		}
		if server.IsPermanent(err) {
			return err
		}
		if err := waitForRetry(ctx, probe.pollInterval); err != nil {
			return fmt.Errorf("wait for Pterodactyl readiness: %w", err)
		}
	}
}
