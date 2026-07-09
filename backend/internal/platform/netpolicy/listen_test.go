package netpolicy

import "testing"

func TestValidateListenerTopology(t *testing.T) {
	tests := []struct {
		name       string
		management string
		peer       string
		allow      bool
		wantErr    bool
	}{
		{name: "loopback management only", management: "127.0.0.1:18080"},
		{name: "ipv6 loopback with peer", management: "[::1]:18080", peer: ":18081"},
		{name: "localhost management", management: "localhost:18080", peer: "0.0.0.0:18081"},
		{name: "wildcard management rejected", management: ":18080", wantErr: true},
		{name: "explicit remote management", management: ":18080", allow: true},
		{name: "same listener port rejected", management: "127.0.0.1:18080", peer: ":18080", wantErr: true},
		{name: "invalid peer", management: "127.0.0.1:18080", peer: "peer", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateListenerTopology(test.management, test.peer, test.allow)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateListenerTopology() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestValidatePeerAPIBase(t *testing.T) {
	for _, test := range []struct {
		name    string
		base    string
		wantErr bool
	}{
		{name: "direct peer listener", base: "http://192.0.2.10:18081/api"},
		{name: "tls reverse proxy", base: "https://steward.example.test/api"},
		{name: "management port", base: "http://192.0.2.10:18080/api", wantErr: true},
		{name: "wrong path", base: "http://192.0.2.10:18081", wantErr: true},
		{name: "credentials", base: "http://user:pass@192.0.2.10:18081/api", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidatePeerAPIBase(test.base, "127.0.0.1:18080")
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidatePeerAPIBase() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
