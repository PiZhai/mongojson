package steward

import (
	"testing"
	"time"

	"mongojson/backend/internal/domain"
)

type fakePeerDiscovery struct {
	status     domain.StewardPeerDiscoveryStatus
	candidates []domain.StewardDiscoveredPeer
}

func (f fakePeerDiscovery) Status() domain.StewardPeerDiscoveryStatus {
	return f.status
}

func (f fakePeerDiscovery) Candidates() []domain.StewardDiscoveredPeer {
	return append([]domain.StewardDiscoveredPeer{}, f.candidates...)
}

func TestPeerDiscoverySnapshotUsesCandidateCountFromSameSnapshot(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(nil, WithPeerDiscovery(fakePeerDiscovery{
		status: domain.StewardPeerDiscoveryStatus{Enabled: true, Running: true, CandidateCount: 99},
		candidates: []domain.StewardDiscoveredPeer{
			{DeviceID: "macbook-main", LastSeenAt: now, ExpiresAt: now.Add(time.Minute)},
			{DeviceID: "linux-lab", LastSeenAt: now, ExpiresAt: now.Add(time.Minute)},
		},
	}))

	status, candidates := service.peerDiscoverySnapshot()
	if status.CandidateCount != 2 || len(candidates) != 2 {
		t.Fatalf("unexpected discovery snapshot: status=%#v candidates=%#v", status, candidates)
	}
	candidates[0].DeviceID = "mutated"
	_, fresh := service.peerDiscoverySnapshot()
	if fresh[0].DeviceID != "macbook-main" {
		t.Fatalf("discovery snapshot should not expose catalog storage")
	}
}

func TestNewServiceUsesDisabledDiscoveryByDefault(t *testing.T) {
	service := NewService(nil)
	status, candidates := service.peerDiscoverySnapshot()
	if status.Enabled || status.Running || len(candidates) != 0 || status.Targets == nil {
		t.Fatalf("unexpected default discovery snapshot: status=%#v candidates=%#v", status, candidates)
	}
}
