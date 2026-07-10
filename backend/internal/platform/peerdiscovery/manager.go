package peerdiscovery

import (
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mongojson/backend/internal/domain"
)

const (
	DefaultListenAddr = "239.255.77.77:18777"
	DefaultInterval   = 15 * time.Second
	DefaultTTL        = 45 * time.Second
	maxCandidates     = 256
)

type Options struct {
	Enabled     bool
	ListenAddr  string
	Targets     []string
	Interval    time.Duration
	TTL         time.Duration
	DeviceID    string
	DeviceName  string
	Platform    string
	PeerAPIBase string
	PublicKey   string
	PrivateKey  string
}

type Manager struct {
	options   Options
	identity  normalizedIdentity
	listenUDP *net.UDPAddr
	targets   []*net.UDPAddr

	mu                    sync.Mutex
	candidates            map[string]domain.StewardDiscoveredPeer
	cancel                context.CancelFunc
	listener              *net.UDPConn
	sender                *net.UDPConn
	lastAnnouncementAt    *time.Time
	lastDiscoveryAt       *time.Time
	lastError             string
	rejectedAnnouncements uint64
	wg                    sync.WaitGroup
	running               atomic.Bool
}

func OptionsFromEnv() (Options, error) {
	hostname, _ := os.Hostname()
	enabled, err := envBool("STEWARD_DISCOVERY_ENABLED", false)
	if err != nil {
		return Options{}, err
	}
	interval, err := envDuration("STEWARD_DISCOVERY_INTERVAL", DefaultInterval)
	if err != nil {
		return Options{}, err
	}
	ttl, err := envDuration("STEWARD_DISCOVERY_TTL", DefaultTTL)
	if err != nil {
		return Options{}, err
	}
	return Options{
		Enabled:     enabled,
		ListenAddr:  envOrDefault("STEWARD_DISCOVERY_LISTEN_ADDR", DefaultListenAddr),
		Targets:     splitCSV(os.Getenv("STEWARD_DISCOVERY_TARGETS")),
		Interval:    interval,
		TTL:         ttl,
		DeviceID:    envOrDefault("STEWARD_AGENT_ID", "local-s1"),
		DeviceName:  envOrDefault("STEWARD_DEVICE_NAME", hostname),
		Platform:    runtime.GOOS,
		PeerAPIBase: os.Getenv("STEWARD_PUBLIC_API_BASE"),
		PublicKey:   os.Getenv("STEWARD_DEVICE_PUBLIC_KEY"),
		PrivateKey:  os.Getenv("STEWARD_DEVICE_PRIVATE_KEY"),
	}, nil
}

func New(options Options) (*Manager, error) {
	options.ListenAddr = strings.TrimSpace(options.ListenAddr)
	if options.ListenAddr == "" {
		options.ListenAddr = DefaultListenAddr
	}
	if options.Interval <= 0 {
		options.Interval = DefaultInterval
	}
	if options.TTL <= 0 {
		options.TTL = DefaultTTL
	}
	manager := &Manager{
		options:    options,
		candidates: map[string]domain.StewardDiscoveredPeer{},
	}
	if !options.Enabled {
		return manager, nil
	}
	if options.TTL < 2*options.Interval {
		return nil, fmt.Errorf("STEWARD_DISCOVERY_TTL must be at least twice STEWARD_DISCOVERY_INTERVAL")
	}
	identity, err := normalizeIdentity(options)
	if err != nil {
		return nil, err
	}
	listenUDP, err := resolveIPv4UDPAddr(options.ListenAddr, true)
	if err != nil {
		return nil, fmt.Errorf("resolve discovery listen address: %w", err)
	}
	targetValues := options.Targets
	if len(targetValues) == 0 {
		targetValues = []string{options.ListenAddr}
	}
	targets := make([]*net.UDPAddr, 0, len(targetValues))
	for _, value := range targetValues {
		target, err := resolveIPv4UDPAddr(value, false)
		if err != nil {
			return nil, fmt.Errorf("resolve discovery target %q: %w", value, err)
		}
		targets = append(targets, target)
	}
	manager.identity = identity
	manager.listenUDP = listenUDP
	manager.targets = targets
	return manager, nil
}

func (m *Manager) Start(parent context.Context) error {
	if m == nil || !m.options.Enabled {
		return nil
	}
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return nil
	}
	listener, err := openListener(m.listenUDP)
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("listen for steward peer discovery: %w", err)
	}
	sender, err := net.ListenUDP("udp4", nil)
	if err != nil {
		_ = listener.Close()
		m.mu.Unlock()
		return fmt.Errorf("open steward discovery sender: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.listener = listener
	m.sender = sender
	m.lastError = ""
	m.running.Store(true)
	m.wg.Add(2)
	m.mu.Unlock()

	go m.readLoop(ctx, listener)
	go m.announceLoop(ctx, sender)
	return nil
}

func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	cancel := m.cancel
	listener := m.listener
	sender := m.sender
	m.cancel = nil
	m.listener = nil
	m.sender = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if listener != nil {
		_ = listener.Close()
	}
	if sender != nil {
		_ = sender.Close()
	}
	m.wg.Wait()
	m.running.Store(false)
}

func (m *Manager) IsRunning() bool {
	return m != nil && m.running.Load()
}

func (m *Manager) Candidates() []domain.StewardDiscoveredPeer {
	if m == nil {
		return []domain.StewardDiscoveredPeer{}
	}
	now := time.Now().UTC()
	m.mu.Lock()
	m.removeExpiredLocked(now)
	items := make([]domain.StewardDiscoveredPeer, 0, len(m.candidates))
	for _, candidate := range m.candidates {
		items = append(items, candidate)
	}
	m.mu.Unlock()
	sort.Slice(items, func(i, j int) bool {
		if items[i].DeviceID == items[j].DeviceID {
			return items[i].PublicKeyFingerprint < items[j].PublicKeyFingerprint
		}
		return items[i].DeviceID < items[j].DeviceID
	})
	return items
}

func (m *Manager) Status() domain.StewardPeerDiscoveryStatus {
	if m == nil {
		return domain.StewardPeerDiscoveryStatus{Targets: []string{}}
	}
	candidates := m.Candidates()
	m.mu.Lock()
	defer m.mu.Unlock()
	targets := make([]string, 0, len(m.targets))
	for _, target := range m.targets {
		targets = append(targets, target.String())
	}
	return domain.StewardPeerDiscoveryStatus{
		Enabled:               m.options.Enabled,
		Running:               m.running.Load(),
		ListenAddr:            m.options.ListenAddr,
		Targets:               targets,
		CandidateCount:        len(candidates),
		RejectedAnnouncements: m.rejectedAnnouncements,
		LastAnnouncementAt:    cloneTime(m.lastAnnouncementAt),
		LastDiscoveryAt:       cloneTime(m.lastDiscoveryAt),
		LastError:             m.lastError,
	}
}

func (m *Manager) readLoop(ctx context.Context, listener *net.UDPConn) {
	defer m.wg.Done()
	buffer := make([]byte, maxAnnouncementSize+1)
	for {
		read, _, err := listener.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() == nil {
				m.fail("read discovery announcement: " + err.Error())
			}
			return
		}
		receivedAt := time.Now().UTC()
		candidate, err := verifyAnnouncement(buffer[:read], receivedAt, m.options.TTL)
		if err != nil {
			m.mu.Lock()
			m.rejectedAnnouncements++
			m.mu.Unlock()
			continue
		}
		if candidate.DeviceID == m.identity.DeviceID && subtle.ConstantTimeCompare([]byte(candidate.PublicKey), []byte(m.identity.PublicKeyText)) == 1 {
			continue
		}
		m.storeCandidate(candidate)
	}
}

func (m *Manager) storeCandidate(candidate domain.StewardDiscoveredPeer) bool {
	key := candidate.DeviceID + "\x00" + candidate.PublicKeyFingerprint
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeExpiredLocked(candidate.LastSeenAt)
	current, exists := m.candidates[key]
	if !exists && len(m.candidates) >= maxCandidates {
		m.rejectedAnnouncements++
		return false
	}
	if !exists || candidate.IssuedAt.After(current.IssuedAt) {
		m.candidates[key] = candidate
	}
	m.lastDiscoveryAt = timePtr(candidate.LastSeenAt)
	return true
}

func (m *Manager) announceLoop(ctx context.Context, sender *net.UDPConn) {
	defer m.wg.Done()
	m.announceOnce(sender)
	ticker := time.NewTicker(m.options.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.announceOnce(sender)
		}
	}
}

func (m *Manager) announceOnce(sender *net.UDPConn) {
	now := time.Now().UTC()
	packet, err := buildSignedAnnouncement(m.identity, now, nil)
	if err != nil {
		m.setLastError(err.Error())
		return
	}
	var failures []string
	for _, target := range m.targets {
		if _, err := sender.WriteToUDP(packet, target); err != nil {
			failures = append(failures, target.String()+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		m.setLastError("send discovery announcement: " + strings.Join(failures, "; "))
		return
	}
	m.mu.Lock()
	m.lastAnnouncementAt = timePtr(now)
	m.lastError = ""
	m.mu.Unlock()
}

func (m *Manager) fail(message string) {
	m.setLastError(message)
	m.running.Store(false)
	m.mu.Lock()
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	log.Printf("steward peer discovery stopped: %s", message)
}

func (m *Manager) setLastError(message string) {
	m.mu.Lock()
	m.lastError = strings.TrimSpace(message)
	m.mu.Unlock()
}

func (m *Manager) removeExpiredLocked(now time.Time) {
	for key, candidate := range m.candidates {
		if !candidate.ExpiresAt.After(now) {
			delete(m.candidates, key)
		}
	}
}

func normalizeIdentity(options Options) (normalizedIdentity, error) {
	deviceID := strings.TrimSpace(options.DeviceID)
	if err := validateTextField("device_id", deviceID, 128); err != nil {
		return normalizedIdentity{}, err
	}
	deviceName := strings.TrimSpace(options.DeviceName)
	if deviceName == "" {
		deviceName = deviceID
	}
	if err := validateTextField("device_name", deviceName, 256); err != nil {
		return normalizedIdentity{}, err
	}
	platform := strings.TrimSpace(options.Platform)
	if platform == "" {
		platform = runtime.GOOS
	}
	if err := validateTextField("platform", platform, 32); err != nil {
		return normalizedIdentity{}, err
	}
	peerAPIBase := strings.TrimRight(strings.TrimSpace(options.PeerAPIBase), "/")
	if err := validateAnnouncementPayload(announcementPayload{
		Protocol: protocolVersion, DeviceID: deviceID, DeviceName: deviceName, Platform: platform,
		PeerAPIBase: peerAPIBase, PublicKey: options.PublicKey, IssuedAtMS: 1, Nonce: "AAAAAAAAAAAAAAAAAAAAAA",
	}); err != nil {
		return normalizedIdentity{}, err
	}
	publicKey, err := decodePublicKey(options.PublicKey)
	if err != nil {
		return normalizedIdentity{}, err
	}
	privateKeyBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(options.PrivateKey))
	if err != nil {
		return normalizedIdentity{}, fmt.Errorf("invalid discovery private key")
	}
	var privateKey ed25519.PrivateKey
	switch len(privateKeyBytes) {
	case ed25519.SeedSize:
		privateKey = ed25519.NewKeyFromSeed(privateKeyBytes)
	case ed25519.PrivateKeySize:
		privateKey = ed25519.PrivateKey(privateKeyBytes)
	default:
		return normalizedIdentity{}, fmt.Errorf("invalid discovery private key")
	}
	derived := privateKey.Public().(ed25519.PublicKey)
	if subtle.ConstantTimeCompare(publicKey, derived) != 1 {
		return normalizedIdentity{}, fmt.Errorf("discovery public key does not match private key")
	}
	return normalizedIdentity{
		DeviceID: deviceID, DeviceName: deviceName, Platform: platform, PeerAPIBase: peerAPIBase,
		PublicKeyText: base64.StdEncoding.EncodeToString(publicKey), PublicKey: publicKey, PrivateKey: privateKey,
	}, nil
}

func resolveIPv4UDPAddr(value string, allowUnspecified bool) (*net.UDPAddr, error) {
	address, err := net.ResolveUDPAddr("udp4", strings.TrimSpace(value))
	if err != nil {
		return nil, err
	}
	if address.Port <= 0 || address.IP == nil || address.IP.To4() == nil {
		return nil, fmt.Errorf("address must contain an IPv4 host and non-zero port")
	}
	if !allowUnspecified && address.IP.IsUnspecified() {
		return nil, fmt.Errorf("target address cannot be unspecified")
	}
	return address, nil
}

func openListener(address *net.UDPAddr) (*net.UDPConn, error) {
	if address.IP.IsMulticast() {
		return net.ListenMulticastUDP("udp4", nil, address)
	}
	return net.ListenUDP("udp4", address)
}

func splitCSV(value string) []string {
	items := []string{}
	seen := map[string]struct{}{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		items = append(items, item)
	}
	return items
}

func envOrDefault(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return parsed, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return parsed, nil
}

func timePtr(value time.Time) *time.Time {
	copy := value
	return &copy
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
