package steward

import (
	"context"
	"fmt"
	"strings"
)

type AutonomyProposalDiscoverer interface {
	Name() string
	Discover(context.Context, int) error
}

type autonomyProposalDiscoverer struct {
	name     string
	discover func(context.Context, int) error
}

func newAutonomyProposalDiscoverer(name string, discover func(context.Context, int) error) AutonomyProposalDiscoverer {
	return autonomyProposalDiscoverer{name: strings.TrimSpace(name), discover: discover}
}

func (d autonomyProposalDiscoverer) Name() string {
	return d.name
}

func (d autonomyProposalDiscoverer) Discover(ctx context.Context, limit int) error {
	if d.discover == nil {
		return fmt.Errorf("autonomy proposal discoverer %s is not initialized", d.name)
	}
	return d.discover(ctx, limit)
}

type autonomyProposalDiscovererRegistry struct {
	order       []string
	discoverers map[string]AutonomyProposalDiscoverer
}

func newAutonomyProposalDiscovererRegistry(discoverers ...AutonomyProposalDiscoverer) *autonomyProposalDiscovererRegistry {
	registry := &autonomyProposalDiscovererRegistry{
		order:       []string{},
		discoverers: map[string]AutonomyProposalDiscoverer{},
	}
	for _, discoverer := range discoverers {
		registry.register(discoverer)
	}
	return registry
}

func (r *autonomyProposalDiscovererRegistry) register(discoverer AutonomyProposalDiscoverer) {
	if r == nil || discoverer == nil {
		return
	}
	name := strings.TrimSpace(discoverer.Name())
	if name == "" {
		return
	}
	if r.discoverers == nil {
		r.discoverers = map[string]AutonomyProposalDiscoverer{}
	}
	if _, exists := r.discoverers[name]; !exists {
		r.order = append(r.order, name)
	}
	r.discoverers[name] = discoverer
}

func (r *autonomyProposalDiscovererRegistry) discover(ctx context.Context, limit int) error {
	if r == nil {
		return nil
	}
	for _, name := range r.order {
		discoverer, ok := r.discoverers[name]
		if !ok || discoverer == nil {
			continue
		}
		if err := discoverer.Discover(ctx, limit); err != nil {
			return fmt.Errorf("discover autonomy proposals with %s: %w", name, err)
		}
	}
	return nil
}
