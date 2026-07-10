package steward

import "mongojson/backend/internal/domain"

type PeerDiscoveryCatalog interface {
	Status() domain.StewardPeerDiscoveryStatus
	Candidates() []domain.StewardDiscoveredPeer
}

type disabledPeerDiscovery struct{}

func (disabledPeerDiscovery) Status() domain.StewardPeerDiscoveryStatus {
	return domain.StewardPeerDiscoveryStatus{Targets: []string{}}
}

func (disabledPeerDiscovery) Candidates() []domain.StewardDiscoveredPeer {
	return []domain.StewardDiscoveredPeer{}
}

func (s *Service) peerDiscoverySnapshot() (domain.StewardPeerDiscoveryStatus, []domain.StewardDiscoveredPeer) {
	if s == nil || s.peerDiscovery == nil {
		disabled := disabledPeerDiscovery{}
		return disabled.Status(), disabled.Candidates()
	}
	candidates := s.peerDiscovery.Candidates()
	status := s.peerDiscovery.Status()
	status.CandidateCount = len(candidates)
	return status, candidates
}
