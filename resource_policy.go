package main

import (
	"context"
	"sync/atomic"

	"github.com/everyapi-ai/everyapi-edge/internal/config"
	"github.com/everyapi-ai/everyapi-edge/internal/console"
	"github.com/everyapi-ai/everyapi-edge/internal/protocol"
)

type drainClient interface {
	BeginDrain()
	CancelDrain()
	DrainState() string
	ActiveRequests() int
	WaitForDrained(context.Context) error
}

type resourcePolicyController struct {
	store        *config.ResourcePolicyStore
	activeClient func() drainClient
	refresh      *metadataRefresh
	manualDrain  atomic.Bool
}

func newResourcePolicyController(store *config.ResourcePolicyStore, activeClient func() drainClient, refresh *metadataRefresh) *resourcePolicyController {
	return &resourcePolicyController{store: store, activeClient: activeClient, refresh: refresh}
}

func (c *resourcePolicyController) Load() (console.ResourceSettings, error) {
	policy, err := c.store.Load()
	if err != nil {
		return console.ResourceSettings{}, err
	}
	state := protocol.DrainStateServing
	activeRequests := 0
	if active := c.active(); active != nil {
		state = active.DrainState()
		activeRequests = active.ActiveRequests()
	} else if c.manualDrain.Load() {
		state = protocol.DrainStateDrained
	}
	return console.ResourceSettings{ResourcePolicy: policy, DrainState: state, ActiveRequests: activeRequests}, nil
}

func (c *resourcePolicyController) Save(ctx context.Context, policy protocol.ResourcePolicy) (settings console.ResourceSettings, err error) {
	active := c.active()
	if active != nil {
		active.BeginDrain()
		defer func() {
			if err != nil {
				active.CancelDrain()
			}
		}()
		if err = active.WaitForDrained(ctx); err != nil {
			return console.ResourceSettings{}, err
		}
	}
	if err = c.store.Save(policy); err != nil {
		return console.ResourceSettings{}, err
	}
	if c.refresh != nil {
		c.refresh.Notify()
	}
	state := protocol.DrainStateServing
	if active != nil {
		state = protocol.DrainStateDrained
	}
	return console.ResourceSettings{ResourcePolicy: policy, DrainState: state}, nil
}

func (c *resourcePolicyController) SetDrain(_ context.Context, enabled bool) (console.ResourceSettings, error) {
	c.manualDrain.Store(enabled)
	if active := c.active(); active != nil {
		if enabled {
			active.BeginDrain()
		} else {
			active.CancelDrain()
		}
	}
	return c.Load()
}

func (c *resourcePolicyController) BeginMaintenance(ctx context.Context) (func(), error) {
	active := c.active()
	if active == nil {
		return func() {}, nil
	}
	wasDraining := c.manualDrain.Load() || active.DrainState() != protocol.DrainStateServing
	active.BeginDrain()
	if err := active.WaitForDrained(ctx); err != nil {
		if !wasDraining {
			active.CancelDrain()
		}
		return nil, err
	}
	return func() {
		if !wasDraining {
			active.CancelDrain()
		}
	}, nil
}

func (c *resourcePolicyController) SessionActive(active drainClient) {
	if active != nil && c.manualDrain.Load() {
		active.BeginDrain()
	}
}

func (c *resourcePolicyController) active() drainClient {
	if c == nil || c.activeClient == nil {
		return nil
	}
	return c.activeClient()
}
