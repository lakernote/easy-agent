package main

import "testing"

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
