package steward

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestAutonomyProposalDiscovererRegistryPreservesOrderAndReplacesByName(t *testing.T) {
	calls := []string{}
	registry := newAutonomyProposalDiscovererRegistry(
		newAutonomyProposalDiscoverer("first", func(_ context.Context, limit int) error {
			calls = append(calls, "old-first")
			return nil
		}),
		newAutonomyProposalDiscoverer("second", func(_ context.Context, limit int) error {
			calls = append(calls, "second")
			return nil
		}),
	)
	registry.register(newAutonomyProposalDiscoverer("first", func(_ context.Context, limit int) error {
		calls = append(calls, "first")
		if limit != 7 {
			t.Fatalf("expected limit 7, got %d", limit)
		}
		return nil
	}))

	if err := registry.discover(context.Background(), 7); err != nil {
		t.Fatalf("discover proposals: %v", err)
	}
	if want := []string{"first", "second"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected discovery order: got %v want %v", calls, want)
	}
}

func TestAutonomyProposalDiscovererRegistryReportsSourceAndStops(t *testing.T) {
	calls := []string{}
	registry := newAutonomyProposalDiscovererRegistry(
		newAutonomyProposalDiscoverer("broken-source", func(context.Context, int) error {
			calls = append(calls, "broken-source")
			return errors.New("scan failed")
		}),
		newAutonomyProposalDiscoverer("later-source", func(context.Context, int) error {
			calls = append(calls, "later-source")
			return nil
		}),
	)

	err := registry.discover(context.Background(), 3)
	if err == nil || !strings.Contains(err.Error(), "broken-source") || !strings.Contains(err.Error(), "scan failed") {
		t.Fatalf("expected source-aware error, got %v", err)
	}
	if want := []string{"broken-source"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("discovery should stop after failure: got %v want %v", calls, want)
	}
}

func TestNewServiceRegistersDefaultAutonomyProposalDiscoverers(t *testing.T) {
	service := NewService(nil)
	want := []string{
		"event-follow-up",
		"stale-task-review",
		"event-knowledge-summary",
		"due-task-reminder",
		"sync-conflict-diagnostics",
	}
	if !reflect.DeepEqual(service.proposalSources.order, want) {
		t.Fatalf("unexpected default discovery order: got %v want %v", service.proposalSources.order, want)
	}
	for _, name := range want {
		if service.proposalSources.discoverers[name] == nil {
			t.Fatalf("missing default discoverer %s", name)
		}
	}
}
