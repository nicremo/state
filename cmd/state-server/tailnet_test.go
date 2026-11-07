package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDesktopAdmitsTailscalePeersOnlyOnTheTailscaleAddress(t *testing.T) {
	handler := desktopLANOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	cases := []struct {
		peer, local string
		want        int
	}{
		{"100.112.165.31:5000", "100.92.239.22:9847", 204},
		{"[fd7a:115c:a1e0::1]:5000", "[fd7a:115c:a1e0::2]:9847", 204},
		// A tailnet peer that reached the LAN address did not come through
		// the tunnel.
		{"100.112.165.31:5000", "192.168.178.83:9847", 403},
		{"8.8.8.8:5000", "100.92.239.22:9847", 403},
		{"100.112.165.31:5000", "", 403},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = tc.peer
		if tc.local != "" {
			local, err := net.ResolveTCPAddr("tcp", tc.local)
			if err != nil {
				t.Fatal(err)
			}
			r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, net.Addr(local)))
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s -> %s got %d, want %d", tc.peer, tc.local, w.Code, tc.want)
		}
	}
}

func TestTailnetURLUsesTheTailscaleIPv4Address(t *testing.T) {
	addresses := func(list ...string) func() ([]net.Addr, error) {
		return func() ([]net.Addr, error) {
			result := make([]net.Addr, 0, len(list))
			for _, cidr := range list {
				ip, network, err := net.ParseCIDR(cidr)
				if err != nil {
					t.Fatal(err)
				}
				network.IP = ip
				result = append(result, network)
			}
			return result, nil
		}
	}
	if got := tailnetURLFrom(addresses("127.0.0.1/8", "192.168.178.83/24", "100.92.239.22/32", "fd7a:115c:a1e0::5/128"), "9847"); got != "https://100.92.239.22:9847" {
		t.Fatalf("url = %q", got)
	}
	if got := tailnetURLFrom(addresses("192.168.178.83/24"), "9847"); got != "" {
		t.Fatalf("url without Tailscale = %q", got)
	}
}
