// Package app contains Edge process orchestration that is independent from concrete gateway, console, and runtime implementations.
package app

import (
	"context"
	"errors"
	"time"
)

type Session interface {
	Run(context.Context) error
	WelcomeReceived() bool
}

type SessionFactory func(context.Context, string) (Session, error)

type Hooks struct {
	Connecting func()
	Active     func(Session)
	Inactive   func()
	Offline    func(error)
	Reconnect  func(time.Time, int)
}

type Reconnector struct {
	Hooks       Hooks
	Wait        func(context.Context, time.Duration) error
	NextBackoff func(time.Duration) time.Duration
	Now         func() time.Time
	IsTerminal  func(error) bool
	OnTerminal  func(error) error
}

func (r Reconnector) Run(ctx context.Context, registrationToken string, factory SessionFactory) error {
	backoff := time.Second
	reconnectAttempt := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		call(r.Hooks.Connecting)
		session, err := factory(ctx, registrationToken)
		if err != nil {
			return err
		}
		if r.Hooks.Active != nil {
			r.Hooks.Active(session)
		}
		runErr := session.Run(ctx)
		call(r.Hooks.Inactive)
		welcomed := session.WelcomeReceived()
		if welcomed {
			registrationToken = ""
		}
		if runErr == nil || errors.Is(runErr, context.Canceled) {
			return runErr
		}
		if r.Hooks.Offline != nil {
			r.Hooks.Offline(runErr)
		}
		if r.IsTerminal != nil && r.IsTerminal(runErr) {
			if r.OnTerminal != nil {
				return r.OnTerminal(runErr)
			}
			return nil
		}
		if welcomed {
			backoff = time.Second
			reconnectAttempt = 0
		}
		reconnectAttempt++
		now := time.Now().UTC()
		if r.Now != nil {
			now = r.Now()
		}
		if r.Hooks.Reconnect != nil {
			r.Hooks.Reconnect(now.Add(backoff), reconnectAttempt)
		}
		if err := r.wait(ctx, backoff); err != nil {
			return err
		}
		backoff = r.nextBackoff(backoff)
	}
}

func (r Reconnector) wait(ctx context.Context, delay time.Duration) error {
	if r.Wait != nil {
		return r.Wait(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (r Reconnector) nextBackoff(current time.Duration) time.Duration {
	if r.NextBackoff != nil {
		return r.NextBackoff(current)
	}
	next := current * 2
	if next > 30*time.Second {
		return 30 * time.Second
	}
	return next
}

func call(callback func()) {
	if callback != nil {
		callback()
	}
}
