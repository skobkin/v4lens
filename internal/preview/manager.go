package preview

import (
	"context"
	"log/slog"
	"sync"

	"git.skobk.in/skobkin/v4lens/internal/domain"
)

// Manager owns one Pipeline per device key and adapts them to the
// profile.StreamOwner contract the apply engine coordinates with.
type Manager struct {
	open Opener
	log  *slog.Logger

	mu        sync.Mutex
	pipelines map[string]*Pipeline
}

// NewManager builds an empty manager.
func NewManager(open Opener, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Manager{open: open, log: log, pipelines: map[string]*Pipeline{}}
}

// PipelineFor returns the pipeline for key, creating it on first use. The
// pipeline does not stream until Start is called (a PipelineFor-created
// pipeline with no active stream answers SuspendForReconfigure with
// owned=false, so applies never wait on an idle preview).
func (m *Manager) PipelineFor(key string) *Pipeline {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.pipelines[key]
	if !ok {
		p = NewPipeline(key, m.open, m.log)
		m.pipelines[key] = p
	}

	return p
}

// Lookup returns the existing pipeline for key, or nil.
func (m *Manager) Lookup(key string) *Pipeline {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.pipelines[key]
}

// StopKey stops and discards one device's pipeline (window closed, device
// gone).
func (m *Manager) StopKey(key string) {
	m.mu.Lock()
	p := m.pipelines[key]
	delete(m.pipelines, key)
	m.mu.Unlock()

	if p != nil {
		p.Stop()
	}
}

// StopAll stops every pipeline (app shutdown).
func (m *Manager) StopAll() {
	m.mu.Lock()
	all := make([]*Pipeline, 0, len(m.pipelines))

	for k, p := range m.pipelines {
		all = append(all, p)
		delete(m.pipelines, k)
	}

	m.mu.Unlock()

	for _, p := range all {
		p.Stop()
	}
}

// SuspendForReconfigure implements profile.StreamOwner: freeze this
// device's preview (if streaming) so the capture transaction can run on
// the stream lane; the returned resume restarts it with the effective
// configuration.
func (m *Manager) SuspendForReconfigure(ctx context.Context, key string) (
	func(context.Context, domain.CaptureConfig) error, bool, error,
) {
	m.mu.Lock()
	p := m.pipelines[key]
	m.mu.Unlock()

	if p == nil {
		return nil, false, nil
	}

	return p.SuspendForReconfigure(ctx, key)
}
