package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

// A grant belongs to one invocation. Closing it also closes CONNECT tunnels.
type NetworkGrant struct {
	domains     map[string]bool
	token       string
	server      *http.Server
	listener    net.Listener
	transport   *http.Transport
	mutex       sync.Mutex
	connections map[net.Conn]bool
	closed      bool
}

func approvedDomains(values []string) (map[string]bool, error) {
	domains := make(map[string]bool)
	for _, value := range values {
		name := strings.ToLower(value)
		if name != value || len(name) > 253 || net.ParseIP(name) != nil || !strings.Contains(name, ".") {
			return nil, fmt.Errorf("domain must be an exact lowercase DNS hostname: %q", value)
		}
		for _, label := range strings.Split(name, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return nil, fmt.Errorf("invalid domain: %q", value)
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return nil, fmt.Errorf("invalid domain: %q", value)
				}
			}
		}
		domains[name] = true
	}
	if len(domains) > 32 {
		return nil, fmt.Errorf("at most 32 domains may be requested")
	}
	return domains, nil
}

func newNetworkGrant(listener net.Listener, domains map[string]bool) (*NetworkGrant, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		listener.Close()
		return nil, err
	}
	grant := &NetworkGrant{domains: domains, token: hex.EncodeToString(secret), listener: listener, connections: make(map[net.Conn]bool)}
	grant.transport = &http.Transport{Proxy: nil, DialContext: grant.dial, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, DisableKeepAlives: true}
	grant.server = &http.Server{Handler: grant, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 64 << 10}
	go grant.server.Serve(listener)
	return grant, nil
}

func (g *NetworkGrant) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || !g.domains[host] || port != "80" && port != "443" {
		return nil, fmt.Errorf("destination denied")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, address := range ips {
		ip := address.IP
		if !publicAddress(ip) {
			return nil, fmt.Errorf("non-public destination denied")
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("domain has no addresses")
	}
	// Dial the checked IP, never resolve the hostname a second time.
	return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(ips[0].IP.String(), port))
}

func (g *NetworkGrant) track(conn net.Conn) bool {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	if g.closed {
		conn.Close()
		return false
	}
	g.connections[conn] = true
	return true
}

func (g *NetworkGrant) untrack(conn net.Conn) {
	conn.Close()
	g.mutex.Lock()
	delete(g.connections, conn)
	g.mutex.Unlock()
}

func (g *NetworkGrant) Close() {
	g.mutex.Lock()
	g.closed = true
	for conn := range g.connections {
		conn.Close()
	}
	g.mutex.Unlock()
	g.server.Close()
	g.transport.CloseIdleConnections()
}

func (g *NetworkGrant) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, password, ok := proxyCredentials(r.Header.Get("Proxy-Authorization"))
	if !ok || user != "garness" || password != g.token {
		w.Header().Set("Proxy-Authenticate", `Basic realm="garness"`)
		http.Error(w, "proxy authentication required", 407)
		return
	}
	if r.Method == http.MethodConnect {
		host, port, err := net.SplitHostPort(r.Host)
		if err != nil || port != "443" || !g.domains[host] {
			http.Error(w, "destination denied", 403)
			return
		}
		remote, err := g.dial(r.Context(), "tcp", r.Host)
		if err != nil {
			http.Error(w, "destination denied or unavailable", 403)
			return
		}
		if !g.track(remote) {
			return
		}
		defer g.untrack(remote)
		client, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		if !g.track(client) {
			return
		}
		defer g.untrack(client)
		buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		if buffered.Flush() != nil {
			return
		}
		client.SetDeadline(time.Now().Add(10 * time.Second))
		hello, err := readClientHello(buffered.Reader, host)
		if err != nil {
			return
		}
		client.SetDeadline(time.Now().Add(5 * time.Minute))
		remote.SetDeadline(time.Now().Add(5 * time.Minute))
		if _, err = remote.Write(hello); err != nil {
			return
		}
		done := make(chan struct{})
		go func() { io.Copy(remote, buffered); remote.Close(); close(done) }()
		io.Copy(client, remote)
		client.Close()
		<-done
		return
	}
	if r.URL.Scheme != "http" || r.URL.User != nil || !g.domains[r.URL.Hostname()] || r.Host != r.URL.Host || r.URL.Port() != "" && r.URL.Port() != "80" {
		http.Error(w, "destination denied", 403)
		return
	}
	request := r.Clone(r.Context())
	request.RequestURI = ""
	stripProxyHeaders(request.Header)
	response, err := g.transport.RoundTrip(request)
	if err != nil {
		http.Error(w, "destination denied or unavailable", 502)
		return
	}
	defer response.Body.Close()
	stripProxyHeaders(response.Header)
	for name, values := range response.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	io.Copy(w, response.Body)
}

func proxyCredentials(header string) (string, string, bool) {
	r := &http.Request{Header: http.Header{"Authorization": []string{header}}}
	return r.BasicAuth()
}

func stripProxyHeaders(header http.Header) {
	for _, value := range strings.Split(header.Get("Connection"), ",") {
		header.Del(strings.TrimSpace(value))
	}
	for _, name := range []string{"Connection", "Proxy-Authorization", "Proxy-Authenticate", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		header.Del(name)
	}
}

func (g *NetworkGrant) URL(address string) string {
	return (&url.URL{Scheme: "http", Host: address, User: url.UserPassword("garness", g.token)}).String()
}

func publicAddress(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() {
		return false
	}
	if address.Is6() && !netip.MustParsePrefix("2000::/3").Contains(address) {
		return false
	}
	for _, value := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "2002::/16"} {
		if netip.MustParsePrefix(value).Contains(address) {
			return false
		}
	}
	return true
}
