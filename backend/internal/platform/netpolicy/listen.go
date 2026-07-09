package netpolicy

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ValidateListenerTopology keeps the privileged management API local unless the
// operator explicitly opts into remote management, and reserves a separate
// listener for peer traffic.
func ValidateListenerTopology(managementAddr string, peerAddr string, allowRemoteManagement bool) error {
	management, err := ParseListenAddress(managementAddr)
	if err != nil {
		return fmt.Errorf("HTTP_ADDR: %w", err)
	}
	if !management.IsLoopback && !allowRemoteManagement {
		return fmt.Errorf("HTTP_ADDR %q exposes the management API; bind it to loopback or set STEWARD_ALLOW_REMOTE_MANAGEMENT=true", managementAddr)
	}

	if strings.TrimSpace(peerAddr) == "" {
		return nil
	}
	peer, err := ParseListenAddress(peerAddr)
	if err != nil {
		return fmt.Errorf("STEWARD_PEER_HTTP_ADDR: %w", err)
	}
	if management.Port == peer.Port {
		return fmt.Errorf("HTTP_ADDR and STEWARD_PEER_HTTP_ADDR must use different ports")
	}
	return nil
}

func ValidatePeerAPIBase(value string, managementAddr string) error {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return fmt.Errorf("STEWARD_PUBLIC_API_BASE must be an absolute http(s) URL")
	}
	if parsed.User != nil {
		return fmt.Errorf("STEWARD_PUBLIC_API_BASE must not contain URL credentials")
	}
	if !strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/api") {
		return fmt.Errorf("STEWARD_PUBLIC_API_BASE path must end with /api")
	}

	management, err := ParseListenAddress(managementAddr)
	if err != nil {
		return fmt.Errorf("HTTP_ADDR: %w", err)
	}
	advertisedPort := 80
	if parsed.Scheme == "https" {
		advertisedPort = 443
	}
	if parsed.Port() != "" {
		advertisedPort, err = strconv.Atoi(parsed.Port())
		if err != nil || advertisedPort < 1 || advertisedPort > 65535 {
			return fmt.Errorf("STEWARD_PUBLIC_API_BASE contains an invalid port")
		}
	}
	if advertisedPort == management.Port {
		return fmt.Errorf("STEWARD_PUBLIC_API_BASE must not advertise the management API port")
	}
	return nil
}

type ListenAddress struct {
	Host       string
	Port       int
	IsLoopback bool
}

func ParseListenAddress(value string) (ListenAddress, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return ListenAddress{}, fmt.Errorf("listen address is required")
	}
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		return ListenAddress{}, fmt.Errorf("invalid listen address %q: %w", value, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return ListenAddress{}, fmt.Errorf("invalid TCP port %q", portText)
	}

	host = strings.TrimSpace(strings.Trim(host, "[]"))
	loopback := strings.EqualFold(host, "localhost")
	if ip := net.ParseIP(host); ip != nil {
		loopback = ip.IsLoopback()
	}
	return ListenAddress{Host: host, Port: port, IsLoopback: loopback}, nil
}
