package main

import (
	"net"
	"testing"
)

func TestListenNetworkFollowsConfiguredAddressFamily(t *testing.T) {
	tests := map[string]string{
		"0.0.0.0:8080":   "tcp4",
		"127.0.0.1:8080": "tcp4",
		"[::]:8080":      "tcp6",
		"localhost:8080": "tcp",
		":8080":          "tcp",
	}
	for address, expected := range tests {
		if actual := listenNetwork(address); actual != expected {
			t.Errorf("listenNetwork(%q) = %q, want %q", address, actual, expected)
		}
	}
}

func TestDisplayListenAddressUsesBoundPort(t *testing.T) {
	tests := []struct {
		configured string
		bound      net.Addr
		expected   string
	}{
		{configured: "0.0.0.0:0", bound: &net.TCPAddr{IP: net.IPv4zero, Port: 43210}, expected: "127.0.0.1:43210"},
		{configured: ":0", bound: &net.TCPAddr{IP: net.IPv6zero, Port: 43211}, expected: "localhost:43211"},
		{configured: "localhost:0", bound: &net.TCPAddr{IP: net.IPv6loopback, Port: 43212}, expected: "localhost:43212"},
		{configured: "[::]:0", bound: &net.TCPAddr{IP: net.IPv6zero, Port: 43213}, expected: "[::1]:43213"},
	}
	for _, test := range tests {
		if actual := displayListenAddress(test.configured, test.bound); actual != test.expected {
			t.Errorf("displayListenAddress(%q, %q) = %q, want %q", test.configured, test.bound, actual, test.expected)
		}
	}
}
