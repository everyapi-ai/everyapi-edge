package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type fakeSession struct {
	err      error
	welcome  bool
	runCount int
}

func (s *fakeSession) Run(context.Context) error {
	s.runCount++
	return s.err
}

func (s *fakeSession) WelcomeReceived() bool { return s.welcome }

func TestReconnectorBurnsRegistrationTokenOnlyAfterWelcome(t *testing.T) {
	transient := errors.New("gateway unavailable")
	sessions := []*fakeSession{
		{err: transient},
		{err: transient, welcome: true},
		{err: context.Canceled, welcome: true},
	}
	var tokens []string
	var waits []time.Duration
	runner := Reconnector{
		Wait: func(context.Context, time.Duration) error {
			waits = append(waits, time.Second)
			return nil
		},
		NextBackoff: func(current time.Duration) time.Duration { return current * 2 },
	}
	err := runner.Run(context.Background(), "one-shot", func(_ context.Context, token string) (Session, error) {
		tokens = append(tokens, token)
		return sessions[len(tokens)-1], nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v", err)
	}
	if !reflect.DeepEqual(tokens, []string{"one-shot", "one-shot", ""}) {
		t.Fatalf("tokens = %#v", tokens)
	}
	if len(waits) != 2 {
		t.Fatalf("wait count = %d", len(waits))
	}
}

func TestReconnectorReportsOrderedLifecycleAndResetsStableBackoff(t *testing.T) {
	transient := errors.New("connection lost")
	sessions := []*fakeSession{{err: transient}, {err: transient, welcome: true}, {err: context.Canceled}}
	var events []string
	var delays []time.Duration
	runner := Reconnector{
		Hooks: Hooks{
			Connecting: func() { events = append(events, "connecting") },
			Active:     func(Session) { events = append(events, "active") },
			Inactive:   func() { events = append(events, "inactive") },
			Offline:    func(error) { events = append(events, "offline") },
			Reconnect: func(_ time.Time, attempt int) {
				events = append(events, "retry")
				if attempt != 1 {
					t.Fatalf("attempt after reset = %d", attempt)
				}
			},
		},
		Wait: func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
		NextBackoff: func(current time.Duration) time.Duration { return current * 2 },
		Now:         func() time.Time { return time.Unix(100, 0).UTC() },
	}
	index := 0
	_ = runner.Run(context.Background(), "", func(context.Context, string) (Session, error) {
		session := sessions[index]
		index++
		return session, nil
	})
	if !reflect.DeepEqual(delays, []time.Duration{time.Second, time.Second}) {
		t.Fatalf("delays = %v", delays)
	}
	wantPrefix := []string{"connecting", "active", "inactive", "offline", "retry"}
	if !reflect.DeepEqual(events[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("events = %#v", events)
	}
}

func TestReconnectorStopsOnTerminalErrorWithoutWaiting(t *testing.T) {
	terminal := errors.New("node revoked")
	terminalCalls := 0
	waitCalls := 0
	runner := Reconnector{
		IsTerminal: func(err error) bool { return errors.Is(err, terminal) },
		OnTerminal: func(err error) error {
			terminalCalls++
			return nil
		},
		Wait: func(context.Context, time.Duration) error {
			waitCalls++
			return nil
		},
	}
	err := runner.Run(context.Background(), "token", func(context.Context, string) (Session, error) {
		return &fakeSession{err: terminal}, nil
	})
	if err != nil || terminalCalls != 1 || waitCalls != 0 {
		t.Fatalf("error=%v terminal=%d waits=%d", err, terminalCalls, waitCalls)
	}
}

func TestReconnectorStopsGracefullyDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := Reconnector{
		Hooks: Hooks{Offline: func(error) { cancel() }},
	}
	err := runner.Run(ctx, "", func(context.Context, string) (Session, error) {
		return &fakeSession{err: errors.New("connection lost")}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v", err)
	}
}
