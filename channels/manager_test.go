package channels

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mosaxiv/clawlet/bus"
)

type stubChannel struct {
	name     string
	startErr error
	sendErr  error
	running  bool
}

func (s *stubChannel) Name() string { return s.name }

func (s *stubChannel) Start(ctx context.Context) error {
	if s.startErr != nil {
		return s.startErr
	}
	s.running = true
	<-ctx.Done()
	s.running = false
	return ctx.Err()
}

func (s *stubChannel) Stop() error {
	s.running = false
	return nil
}

func (s *stubChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	return s.sendErr
}

func (s *stubChannel) IsRunning() bool { return s.running }

func TestManagerStartAll_RecordsStartError(t *testing.T) {
	b := bus.New(16)
	m := NewManager(b)
	m.Add(&stubChannel{name: "stub", startErr: errors.New("start failed")})

	ctx := t.Context()
	if err := m.StartAll(ctx); err != nil {
		t.Fatalf("StartAll returned error: %v", err)
	}

	waitFor(t, 600*time.Millisecond, func() bool {
		st := m.Status()
		row := st["stub"]
		_, ok := row["lastError"]
		return ok
	})
}

func TestManagerDispatchOutbound_RecordsSendError(t *testing.T) {
	b := bus.New(16)
	m := NewManager(b)
	m.Add(&stubChannel{name: "stub", sendErr: errors.New("send failed")})

	ctx := t.Context()
	if err := m.StartAll(ctx); err != nil {
		t.Fatalf("StartAll returned error: %v", err)
	}

	if err := b.PublishOutbound(ctx, bus.OutboundMessage{
		Channel: "stub",
		ChatID:  "c1",
		Content: "hello",
	}); err != nil {
		t.Fatalf("PublishOutbound failed: %v", err)
	}

	waitFor(t, 600*time.Millisecond, func() bool {
		st := m.Status()
		row := st["stub"]
		last, ok := row["lastError"]
		return ok && last == "send failed"
	})
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
