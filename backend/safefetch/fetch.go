// Package safefetch implements bounded, public HTTPS fetches. DNS answers are
// checked at dial time, so URL validation cannot be bypassed by DNS rebinding.
package safefetch

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/domain"
)

var denied = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2001:2::/48"), netip.MustParsePrefix("2001:10::/28"), netip.MustParsePrefix("2001:20::/28"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("2002::/16"),
}

func publicIP(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified() {
		return false
	}
	for _, p := range denied {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

func validateURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 4096 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(raw, "\\\r\n\t ") || (u.Port() != "" && u.Port() != "443") {
		return nil, errors.New("a public HTTPS URL is required")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return nil, errors.New("private hosts are not allowed")
	}
	if ip, e := netip.ParseAddr(host); e == nil && !publicIP(ip) {
		return nil, errors.New("private addresses are not allowed")
	}
	return u, nil
}

type cached struct {
	client  core.OAuthClient
	expires time.Time
}
type Fetcher struct {
	client *http.Client
	mu     sync.Mutex
	cache  map[string]cached
}

func New() *Fetcher {
	transport := &http.Transport{
		Proxy: nil, MaxIdleConns: 16, MaxConnsPerHost: 4, IdleConnTimeout: 30 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 8 * time.Second,
		MaxResponseHeaderBytes: 32 << 10,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, e := net.SplitHostPort(address)
			if e != nil {
				return nil, e
			}
			ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if e != nil || len(ips) == 0 {
				return nil, errors.New("destination cannot be resolved")
			}
			for _, ip := range ips {
				if !publicIP(ip) {
					return nil, errors.New("destination is not public")
				}
			}
			var last error
			for _, ip := range ips {
				conn, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if e == nil {
					return conn, nil
				}
				last = e
			}
			return nil, last
		},
	}
	return &Fetcher{cache: map[string]cached{}, client: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (f *Fetcher) fetch(ctx context.Context, raw string, limit int64) ([]byte, string, error) {
	if _, e := validateURL(raw); e != nil {
		return nil, "", e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return nil, "", errors.New("invalid URL")
	}
	req.Header.Set("User-Agent", "MoneyBags/1.0")
	resp, e := f.client.Do(req)
	if e != nil {
		return nil, "", errors.New("external download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", errors.New("external download unavailable")
	}
	if resp.ContentLength > limit {
		return nil, "", errors.New("external file exceeds size limit")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return nil, "", errors.New("external download incomplete")
	}
	if int64(len(b)) > limit {
		return nil, "", errors.New("external file exceeds size limit")
	}
	return b, http.DetectContentType(b), nil
}

func (f *Fetcher) Fetch(ctx context.Context, raw string) ([]byte, string, error) {
	return f.fetch(ctx, raw, domain.MaxFileBytes)
}

func (f *Fetcher) Resolve(ctx context.Context, id string) (core.OAuthClient, error) {
	f.mu.Lock()
	hit, ok := f.cache[id]
	f.mu.Unlock()
	if ok && time.Now().Before(hit.expires) {
		return hit.client, nil
	}
	var zero core.OAuthClient
	u, e := validateURL(id)
	if e != nil || u.Path == "" || u.Path == "/" || len(id) > 2048 {
		return zero, errors.New("invalid client metadata URL")
	}
	b, _, e := f.fetch(ctx, id, 64<<10)
	if e != nil {
		return zero, e
	}
	result, e := core.ParseOAuthClientMetadata(id, b)
	if e != nil {
		return zero, e
	}
	f.mu.Lock()
	if len(f.cache) >= 256 {
		clear(f.cache)
	}
	f.cache[id] = cached{result, time.Now().Add(5 * time.Minute)}
	f.mu.Unlock()
	return result, nil
}

var _ core.FileFetcher = (*Fetcher)(nil)
var _ core.OAuthClientResolver = (*Fetcher)(nil)
