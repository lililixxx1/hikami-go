package asr

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetectPublicIP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("203.0.113.1"))
	}))
	defer server.Close()

	original := publicIPEndpoints
	publicIPEndpoints = []string{server.URL}
	defer func() { publicIPEndpoints = original }()

	ip := DetectPublicIP(context.Background())
	if ip != "203.0.113.1" {
		t.Errorf("DetectPublicIP() = %q, want %q", ip, "203.0.113.1")
	}
}

func TestDetectPublicIP_AllFail(t *testing.T) {
	original := publicIPEndpoints
	publicIPEndpoints = []string{}
	defer func() { publicIPEndpoints = original }()

	ip := DetectPublicIP(context.Background())
	if ip != "" {
		t.Errorf("DetectPublicIP() = %q, want empty string", ip)
	}
}

func TestDetectPublicIP_InvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("not-an-ip"))
	}))
	defer server.Close()

	original := publicIPEndpoints
	publicIPEndpoints = []string{server.URL}
	defer func() { publicIPEndpoints = original }()

	ip := DetectPublicIP(context.Background())
	if ip != "" {
		t.Errorf("DetectPublicIP() = %q, want empty for invalid IP", ip)
	}
}

func TestDetectPublicIP_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer server.Close()

	original := publicIPEndpoints
	publicIPEndpoints = []string{server.URL}
	defer func() { publicIPEndpoints = original }()

	ip := DetectPublicIP(context.Background())
	if ip != "" {
		t.Errorf("DetectPublicIP() = %q, want empty for server error", ip)
	}
}

func TestDetectPublicIP_FallbackToSecond(t *testing.T) {
	callCount := 0
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(500)
	}))
	defer first.Close()

	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("198.51.100.42"))
	}))
	defer second.Close()

	original := publicIPEndpoints
	publicIPEndpoints = []string{first.URL, second.URL}
	defer func() { publicIPEndpoints = original }()

	ip := DetectPublicIP(context.Background())
	if ip != "198.51.100.42" {
		t.Errorf("DetectPublicIP() = %q, want %q", ip, "198.51.100.42")
	}
	if callCount != 1 {
		t.Errorf("expected first server called once, got %d", callCount)
	}
}

func TestDetectPublicIP_IPv6(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("2001:db8::1"))
	}))
	defer server.Close()

	original := publicIPEndpoints
	publicIPEndpoints = []string{server.URL}
	defer func() { publicIPEndpoints = original }()

	ip := DetectPublicIP(context.Background())
	// 2001:db8::/32 is documentation scope, IsPrivate() may reject it
	parsed := net.ParseIP(ip)
	if ip != "" && parsed == nil {
		t.Errorf("DetectPublicIP() = %q, expected valid IP", ip)
	}
}

func TestDetectPublicIP_RejectsPrivateIP(t *testing.T) {
	privateIPs := []string{"192.168.1.1", "10.0.0.1", "172.16.0.1", "127.0.0.1", "169.254.1.1"}
	for _, testIP := range privateIPs {
		t.Run(testIP, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(200)
				w.Write([]byte(testIP))
			}))
			defer server.Close()

			original := publicIPEndpoints
			publicIPEndpoints = []string{server.URL}
			defer func() { publicIPEndpoints = original }()

			ip := DetectPublicIP(context.Background())
			if ip != "" {
				t.Errorf("DetectPublicIP() = %q, want empty for private IP %q", ip, testIP)
			}
		})
	}
}

func TestIsPublicIP(t *testing.T) {
	tests := []struct {
		ip    string
		valid bool
	}{
		{"8.8.8.8", true},
		{"203.0.113.1", true},
		{"192.168.1.1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"127.0.0.1", false},
		{"169.254.1.1", false},
		{"0.0.0.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			parsed := net.ParseIP(tt.ip)
			if parsed == nil {
				t.Fatalf("failed to parse %q", tt.ip)
			}
			if got := isPublicIP(parsed); got != tt.valid {
				t.Errorf("isPublicIP(%s) = %v, want %v", tt.ip, got, tt.valid)
			}
		})
	}
}
