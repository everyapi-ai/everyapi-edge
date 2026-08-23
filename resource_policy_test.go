package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/everyapi-ai/everyapi-edge/internal/config"
	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

type fakeDrainClient struct {
	beginCalls  int
	cancelCalls int
	state       string
	active      int
	wait        func(context.Context) error
}

func (f *fakeDrainClient) BeginDrain() {
	f.beginCalls++
	f.state = protocol.DrainStateDraining
}

func (f *fakeDrainClient) CancelDrain() {
	f.cancelCalls++
	f.state = protocol.DrainStateServing
}

func (f *fakeDrainClient) DrainState() string  { return f.state }
func (f *fakeDrainClient) ActiveRequests() int { return f.active }
func (f *fakeDrainClient) WaitForDrained(ctx context.Context) error {
	if f.wait != nil {
		return f.wait(ctx)
	}
	f.state = protocol.DrainStateDrained
	f.active = 0
	return nil
}

func testResourcePolicy() protocol.ResourcePolicy {
	return protocol.ResourcePolicy{
		Text:   protocol.RuntimeResourcePolicy{MaxConcurrent: 4},
		Image:  protocol.RuntimeResourcePolicy{MaxConcurrent: 1},
		Speech: protocol.RuntimeResourcePolicy{MaxConcurrent: 2},
		Video:  protocol.RuntimeResourcePolicy{MaxConcurrent: 1},
		Render: protocol.RuntimeResourcePolicy{MaxConcurrent: 1},
		Rerank: protocol.RuntimeResourcePolicy{MaxConcurrent: 2},
	}
}

func TestResourcePolicyControllerDrainsBeforePersistingAndRefreshesSession(t *testing.T) {
	store := config.NewResourcePolicyStore(filepath.Join(t.TempDir(), "policy.json"), testResourcePolicy(), 16)
	active := &fakeDrainClient{state: protocol.DrainStateServing, active: 2}
	refresh := newMetadataRefresh()
	controller := newResourcePolicyController(store, func() drainClient { return active }, refresh)
	next := testResourcePolicy()
	next.Text.MaxConcurrent = 8

	settings, err := controller.Save(context.Background(), next)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if active.beginCalls != 1 || active.cancelCalls != 0 {
		t.Fatalf("drain calls = begin %d cancel %d", active.beginCalls, active.cancelCalls)
	}
	if settings.ResourcePolicy != next || settings.DrainState != protocol.DrainStateDrained {
		t.Fatalf("settings = %#v", settings)
	}
	if got, err := store.Load(); err != nil || got != next {
		t.Fatalf("stored = %#v, err = %v", got, err)
	}
	select {
	case <-refresh.changes:
	default:
		t.Fatal("session refresh was not requested")
	}
}

func TestResourcePolicyControllerRestoresServingWhenSaveFails(t *testing.T) {
	store := config.NewResourcePolicyStore(filepath.Join(t.TempDir(), "policy.json"), testResourcePolicy(), 8)
	active := &fakeDrainClient{state: protocol.DrainStateServing, active: 1}
	controller := newResourcePolicyController(store, func() drainClient { return active }, newMetadataRefresh())
	invalid := testResourcePolicy()
	invalid.Image.ReserveVRAMMB = 9 * 1024

	if _, err := controller.Save(context.Background(), invalid); err == nil {
		t.Fatal("Save invalid policy succeeded")
	}
	if active.beginCalls != 1 || active.cancelCalls != 1 || active.state != protocol.DrainStateServing {
		t.Fatalf("drain rollback = begin %d cancel %d state %q", active.beginCalls, active.cancelCalls, active.state)
	}
}

func TestResourcePolicyControllerPersistsManualDrainAcrossSessionReplacement(t *testing.T) {
	store := config.NewResourcePolicyStore(filepath.Join(t.TempDir(), "policy.json"), testResourcePolicy(), 8)
	var active drainClient
	controller := newResourcePolicyController(store, func() drainClient { return active }, newMetadataRefresh())

	settings, err := controller.SetDrain(context.Background(), true)
	if err != nil || settings.DrainState != protocol.DrainStateDrained {
		t.Fatalf("SetDrain without session = %#v, %v", settings, err)
	}
	next := &fakeDrainClient{state: protocol.DrainStateServing}
	active = next
	controller.SessionActive(next)
	if next.beginCalls != 1 {
		t.Fatalf("replacement BeginDrain calls = %d, want 1", next.beginCalls)
	}
	if _, err := controller.SetDrain(context.Background(), false); err != nil {
		t.Fatalf("cancel drain: %v", err)
	}
	if next.cancelCalls != 1 {
		t.Fatalf("CancelDrain calls = %d, want 1", next.cancelCalls)
	}
}

func TestResourcePolicyControllerMaintenanceDrainResumesServing(t *testing.T) {
	store := config.NewResourcePolicyStore(filepath.Join(t.TempDir(), "policy.json"), testResourcePolicy(), 8)
	active := &fakeDrainClient{state: protocol.DrainStateServing, active: 1}
	controller := newResourcePolicyController(store, func() drainClient { return active }, newMetadataRefresh())

	resume, err := controller.BeginMaintenance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if active.state != protocol.DrainStateDrained || active.beginCalls != 1 {
		t.Fatalf("maintenance drain = state %q begin %d", active.state, active.beginCalls)
	}
	resume()
	if active.state != protocol.DrainStateServing || active.cancelCalls != 1 {
		t.Fatalf("maintenance resume = state %q cancel %d", active.state, active.cancelCalls)
	}
}

func TestResourcePolicyControllerMaintenancePreservesManualDrain(t *testing.T) {
	store := config.NewResourcePolicyStore(filepath.Join(t.TempDir(), "policy.json"), testResourcePolicy(), 8)
	active := &fakeDrainClient{state: protocol.DrainStateDrained}
	controller := newResourcePolicyController(store, func() drainClient { return active }, newMetadataRefresh())
	controller.manualDrain.Store(true)

	resume, err := controller.BeginMaintenance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	resume()
	if active.cancelCalls != 0 || active.state != protocol.DrainStateDrained {
		t.Fatalf("manual drain was cleared: state %q cancel %d", active.state, active.cancelCalls)
	}
}

func TestLogTeeActiveDrainClientReturnsNilWithoutGatewaySession(t *testing.T) {
	logSink := &logTee{}
	if active := logSink.activeDrainClient(); active != nil {
		t.Fatalf("active client = %#v, want nil", active)
	}
}
