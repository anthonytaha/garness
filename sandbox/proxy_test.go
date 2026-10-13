package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDomainValidation(t *testing.T) {
	for _, name := range []string{"*.example.com", "example.com:443", "https://example.com", "127.0.0.1", "EXAMPLE.com", "example.com.", "localhost", "a..com", "-a.com"} {
		if _, err := approvedDomains([]string{name}); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
	domains, err := approvedDomains([]string{"example.com"})
	if err != nil || domains["sub.example.com"] {
		t.Fatal(domains, err)
	}
}

func TestProxyDeniesUnapprovedDestinations(t *testing.T) {
	domains, _ := approvedDomains([]string{"example.com"})
	grant := &NetworkGrant{domains: domains, token: "token"}
	for _, destination := range []string{"http://other.com", "http://sub.example.com", "http://example.com:8080", "https://example.com"} {
		request := httptest.NewRequest("GET", destination, nil)
		request.SetBasicAuth("garness", "token")
		request.Header.Set("Proxy-Authorization", request.Header.Get("Authorization"))
		response := httptest.NewRecorder()
		grant.ServeHTTP(response, request)
		if response.Code != 403 {
			t.Fatalf("%s: %d", destination, response.Code)
		}
	}
	response := httptest.NewRecorder()
	grant.ServeHTTP(response, httptest.NewRequest("GET", "http://example.com", nil))
	if response.Code != 407 {
		t.Fatal(response.Code)
	}
	if _, err := grant.dial(context.Background(), "tcp", "other.com:443"); err == nil {
		t.Fatal("dial bypassed domain policy")
	}
}

func TestClientHelloDomainCheck(t *testing.T) {
	for _, name := range []string{"example.com", "other.com"} {
		client, server := net.Pipe()
		connection := tls.Client(client, &tls.Config{ServerName: name})
		go func() { connection.Handshake(); connection.Close() }()
		server.SetDeadline(time.Now().Add(time.Second))
		_, err := readClientHello(bufio.NewReader(server), "example.com")
		server.Close()
		if (err == nil) != (name == "example.com") {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := readClientHello(bufio.NewReader(bytes.NewBufferString("GET / HTTP/1.1\r\n")), "example.com"); err == nil {
		t.Fatal("accepted non-TLS tunnel")
	}
}

func TestNetworkGrantLifecycle(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grant, err := newNetworkGrant(listener, map[string]bool{"example.com": true})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 407 {
		t.Fatal(response.StatusCode)
	}
	a, b := net.Pipe()
	defer b.Close()
	if !grant.track(a) {
		t.Fatal("track failed")
	}
	grant.Close()
	if _, err := b.Write([]byte("x")); err == nil {
		t.Fatal("tunnel survived grant")
	}
	c, d := net.Pipe()
	defer d.Close()
	if grant.track(c) {
		t.Fatal("closed grant accepted tunnel")
	}
}

func TestProxyRejectsPrivateAddresses(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "100.100.100.200", "192.0.2.1", "::1", "::ffff:127.0.0.1", "fc00::1", "64:ff9b::7f00:1", "2002:7f00:1::"} {
		if publicAddress(net.ParseIP(address)) {
			t.Errorf("accepted %s", address)
		}
	}
	for _, address := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if !publicAddress(net.ParseIP(address)) {
			t.Errorf("denied %s", address)
		}
	}
	grant := &NetworkGrant{domains: map[string]bool{"localhost": true}}
	if _, err := grant.dial(context.Background(), "tcp", "localhost:80"); err == nil {
		t.Fatal("resolved localhost accepted")
	}
}
