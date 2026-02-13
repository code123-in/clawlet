package channels

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/mosaxiv/clawlet/bus"
)

type Manager struct {
	bus      *bus.Bus
	channels map[string]Channel

	mu                 sync.RWMutex
	running            bool
	stopOnce           sync.Once
	lastErrorByChannel map[string]string
}

func NewManager(b *bus.Bus) *Manager {
	return &Manager{
		bus:                b,
		channels:           map[string]Channel{},
		lastErrorByChannel: map[string]string{},
	}
}

func (m *Manager) Add(ch Channel) {
	if ch == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[ch.Name()] = ch
}

func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}
	m.running = true

	chs := make([]Channel, 0, len(m.channels))
	for _, ch := range m.channels {
		chs = append(chs, ch)
	}
	m.mu.Unlock()

	// Start outbound dispatcher
	go m.dispatchOutbound(ctx)

	// Start channels
	for _, ch := range chs {
		m.setChannelError(ch.Name(), "")
		go func() {
			err := ch.Start(ctx)
			// Context cancellation on shutdown is expected.
			if err == nil || errors.Is(err, context.Canceled) {
				return
			}
			m.setChannelError(ch.Name(), err.Error())
			log.Printf("channels: %s stopped with error: %v", ch.Name(), err)
		}()
	}
	return nil
}

func (m *Manager) StopAll() error {
	m.stopOnce.Do(func() {
		m.mu.Lock()
		m.running = false
		chs := make([]Channel, 0, len(m.channels))
		for _, ch := range m.channels {
			chs = append(chs, ch)
		}
		m.mu.Unlock()

		for _, ch := range chs {
			if err := ch.Stop(); err != nil {
				m.setChannelError(ch.Name(), err.Error())
				log.Printf("channels: failed to stop %s: %v", ch.Name(), err)
			}
		}
	})
	return nil
}

func (m *Manager) Status() map[string]map[string]any {
	out := map[string]map[string]any{}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for name, ch := range m.channels {
		row := map[string]any{
			"running": ch.IsRunning(),
		}
		if last, ok := m.lastErrorByChannel[name]; ok && last != "" {
			row["lastError"] = last
		}
		out[name] = row
	}
	return out
}

func (m *Manager) dispatchOutbound(ctx context.Context) {
	for {
		msg, err := m.bus.ConsumeOutbound(ctx)
		if err != nil {
			return
		}
		m.mu.RLock()
		ch := m.channels[msg.Channel]
		m.mu.RUnlock()
		if ch == nil {
			// Unknown channel; drop.
			continue
		}
		if err := ch.Send(ctx, msg); err != nil && !errors.Is(err, context.Canceled) {
			m.setChannelError(msg.Channel, err.Error())
			log.Printf("channels: outbound send failed via %s: %v", msg.Channel, err)
		}
	}
}

func (m *Manager) Require(name string) (Channel, error) {
	m.mu.RLock()
	ch := m.channels[name]
	m.mu.RUnlock()
	if ch == nil {
		return nil, fmt.Errorf("channel not found: %s", name)
	}
	return ch, nil
}

func (m *Manager) setChannelError(name, msg string) {
	if name == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if msg == "" {
		delete(m.lastErrorByChannel, name)
		return
	}
	m.lastErrorByChannel[name] = msg
}
