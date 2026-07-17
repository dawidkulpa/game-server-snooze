package readiness

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"dkulpa.eu/game-server-snooze/pkg/server"
)

type stateController struct {
	mu     sync.Mutex
	states []server.State
	err    error
	calls  int
}

func (controller *stateController) Status(context.Context) (server.State, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.calls++
	if controller.err != nil {
		return "", controller.err
	}
	if len(controller.states) == 0 {
		return server.StateOffline, nil
	}
	state := controller.states[0]
	if len(controller.states) > 1 {
		controller.states = controller.states[1:]
	}
	return state, nil
}

func (*stateController) Start(context.Context) error { return nil }
func (*stateController) Stop(context.Context) error  { return nil }

func TestPterodactylProbeRequiresRunningState(t *testing.T) {
	for _, state := range []server.State{server.StateOffline, server.StateStarting, server.StateStopping} {
		controller := &stateController{states: []server.State{state}}
		probe, err := NewPterodactylProbe(controller, time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if err := probe.Probe(context.Background()); err == nil {
			t.Fatalf("Probe() accepted state %q", state)
		}
	}
	controller := &stateController{states: []server.State{server.StateRunning}}
	probe, err := NewPterodactylProbe(controller, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := probe.Probe(context.Background()); err != nil {
		t.Fatalf("Probe() running error = %v", err)
	}
}

func TestPterodactylProbeWaitsForRunningState(t *testing.T) {
	controller := &stateController{states: []server.State{server.StateStarting, server.StateStarting, server.StateRunning}}
	probe, err := NewPterodactylProbe(controller, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := probe.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady() error = %v", err)
	}
	if controller.calls != 3 {
		t.Fatalf("Status() calls = %d, want 3", controller.calls)
	}
}

func TestPterodactylProbeMarksStatusFailuresAsInconclusive(t *testing.T) {
	want := errors.New("status transport failed")
	controller := &stateController{err: want}
	probe, err := NewPterodactylProbe(controller, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	probeErr := probe.Probe(context.Background())
	if !errors.Is(probeErr, want) {
		t.Fatalf("Probe() error = %v, want wrapped status failure", probeErr)
	}
	var inconclusive interface{ ReadinessInconclusive() bool }
	if !errors.As(probeErr, &inconclusive) || !inconclusive.ReadinessInconclusive() {
		t.Fatalf("Probe() status error is not marked inconclusive: %v", probeErr)
	}
}

func TestPterodactylProbePropagatesPermanentStatusError(t *testing.T) {
	want := errors.New("status failed")
	controller := &stateController{err: permanentStatusError{want}}
	probe, err := NewPterodactylProbe(controller, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := probe.WaitReady(context.Background()); !errors.Is(err, want) {
		t.Fatalf("WaitReady() error = %v, want wrapped permanent error", err)
	}
}

type permanentStatusError struct{ error }

func (status permanentStatusError) Unwrap() error { return status.error }
func (permanentStatusError) Permanent() bool      { return true }
