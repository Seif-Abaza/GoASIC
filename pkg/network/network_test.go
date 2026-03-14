package network

import (
	"net"
	"testing"
)

// ── cidrHosts ─────────────────────────────────────────────────────────────────

func TestCIDRHosts_Slash24(t *testing.T) {
	hosts, err := cidrHosts("192.168.1.0/24")
	if err != nil {
		t.Fatalf("cidrHosts /24: %v", err)
	}
	// /24 has 256 addresses — 1 network — 1 broadcast = 254 hosts
	if len(hosts) != 254 {
		t.Errorf("cidrHosts /24: got %d hosts, want 254", len(hosts))
	}
	// First host should be .1
	if hosts[0] != "192.168.1.1" {
		t.Errorf("first host = %q, want '192.168.1.1'", hosts[0])
	}
	// Last host should be .254
	if hosts[len(hosts)-1] != "192.168.1.254" {
		t.Errorf("last host = %q, want '192.168.1.254'", hosts[len(hosts)-1])
	}
}

func TestCIDRHosts_Slash30(t *testing.T) {
	hosts, err := cidrHosts("10.0.0.0/30")
	if err != nil {
		t.Fatalf("cidrHosts /30: %v", err)
	}
	// /30: 4 addresses — 1 network (10.0.0.0) — 1 broadcast (10.0.0.3) = 2 hosts
	if len(hosts) != 2 {
		t.Errorf("cidrHosts /30: got %d hosts, want 2", len(hosts))
	}
	if hosts[0] != "10.0.0.1" {
		t.Errorf("first host = %q, want '10.0.0.1'", hosts[0])
	}
	if hosts[1] != "10.0.0.2" {
		t.Errorf("second host = %q, want '10.0.0.2'", hosts[1])
	}
}

func TestCIDRHosts_Slash29(t *testing.T) {
	hosts, err := cidrHosts("172.16.0.0/29")
	if err != nil {
		t.Fatalf("cidrHosts /29: %v", err)
	}
	// /29: 8 addresses — 1 network — 1 broadcast = 6 hosts
	if len(hosts) != 6 {
		t.Errorf("cidrHosts /29: got %d hosts, want 6", len(hosts))
	}
}

func TestCIDRHosts_Slash16(t *testing.T) {
	hosts, err := cidrHosts("10.0.0.0/16")
	if err != nil {
		t.Fatalf("cidrHosts /16: %v", err)
	}
	// /16 = 65536 - 2 = 65534 hosts
	if len(hosts) != 65534 {
		t.Errorf("cidrHosts /16: got %d hosts, want 65534", len(hosts))
	}
}

func TestCIDRHosts_InvalidCIDR(t *testing.T) {
	_, err := cidrHosts("not-a-cidr")
	if err == nil {
		t.Error("cidrHosts invalid CIDR should return error")
	}
}

func TestCIDRHosts_InvalidCIDR2(t *testing.T) {
	_, err := cidrHosts("999.999.999.0/24")
	if err == nil {
		t.Error("cidrHosts invalid IP in CIDR should return error")
	}
}

func TestCIDRHosts_AllHostsAreValidIPs(t *testing.T) {
	hosts, err := cidrHosts("192.168.5.0/24")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if net.ParseIP(h) == nil {
			t.Errorf("cidrHosts produced invalid IP: %q", h)
		}
	}
}

func TestCIDRHosts_NoNetworkNoBroadcast(t *testing.T) {
	hosts, err := cidrHosts("192.168.1.0/24")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hosts {
		if h == "192.168.1.0" || h == "192.168.1.255" {
			t.Errorf("cidrHosts included network/broadcast address: %q", h)
		}
	}
}

// ── nextIP ────────────────────────────────────────────────────────────────────

func TestNextIP(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"192.168.1.0", "192.168.1.1"},
		{"192.168.1.254", "192.168.1.255"},
		{"192.168.1.255", "192.168.2.0"},
		{"10.0.0.0", "10.0.0.1"},
		{"255.255.255.254", "255.255.255.255"},
	}
	for _, c := range cases {
		ip := net.ParseIP(c.input).To4()
		got := nextIP(ip)
		if got.String() != c.want {
			t.Errorf("nextIP(%q) = %q, want %q", c.input, got.String(), c.want)
		}
	}
}

// ── containsAny ──────────────────────────────────────────────────────────────

func TestContainsAny(t *testing.T) {
	if !containsAny("connection refused by host", "connection refused", "timeout") {
		t.Error("expected containsAny to match 'connection refused'")
	}
	if !containsAny("i/o timeout occurred", "connection refused", "i/o timeout") {
		t.Error("expected containsAny to match 'i/o timeout'")
	}
	if containsAny("everything is fine", "connection refused", "i/o timeout") {
		t.Error("containsAny should not match in positive string")
	}
	if containsAny("", "connection refused") {
		t.Error("containsAny on empty string should be false")
	}
}

// ── isNotFound ────────────────────────────────────────────────────────────────

func TestIsNotFound(t *testing.T) {
	if isNotFound(nil) {
		t.Error("isNotFound(nil) should be false")
	}
}

// ── NewScanner concurrency bounds ─────────────────────────────────────────────

func TestNewScanner_ConcurrencyBounds(t *testing.T) {
	s := NewScanner([]string{"192.168.1.1"}, 0)
	if s.maxConcurrent < 1 {
		t.Error("maxConcurrent should be at least 1")
	}
	s2 := NewScanner([]string{}, 9999)
	if s2.maxConcurrent > 512 {
		t.Error("maxConcurrent should be capped at 512")
	}
}

// ── NewSubnetScanner ──────────────────────────────────────────────────────────

func TestNewSubnetScanner_ValidCIDR(t *testing.T) {
	s, err := NewSubnetScanner("192.168.1.0/30", 10)
	if err != nil {
		t.Fatalf("NewSubnetScanner: %v", err)
	}
	if len(s.hosts) != 2 {
		t.Errorf("expected 2 hosts for /30, got %d", len(s.hosts))
	}
}

func TestNewSubnetScanner_InvalidCIDR(t *testing.T) {
	_, err := NewSubnetScanner("invalid", 10)
	if err == nil {
		t.Error("NewSubnetScanner invalid CIDR should return error")
	}
}
