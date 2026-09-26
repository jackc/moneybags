package safefetch

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func TestPrivateAndSpecialDestinations(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.2", "169.254.169.254", "100.100.100.200", "0.0.0.0", "::1", "fd00::1", "fe80::1", "::ffff:127.0.0.1", "64:ff9b::7f00:1", "198.18.0.1"} {
		if publicIP(netip.MustParseAddr(s)) {
			t.Errorf("allowed %s", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !publicIP(netip.MustParseAddr(s)) {
			t.Errorf("rejected %s", s)
		}
	}
}
func TestURLRestrictions(t *testing.T) {
	for _, s := range []string{"http://example.com/file", "file:///etc/passwd", "https://localhost/file", "https://127.0.0.1/file", "https://169.254.169.254/", "https://user:password@example.com/", "https://example.com:8443/", "https://example.com/file#fragment", "https://x.internal/"} {
		if _, e := validateURL(s); e == nil {
			t.Errorf("allowed %q", s)
		}
	}
	if _, e := validateURL("https://example.com/receipt?signature=abc"); e != nil {
		t.Fatal(e)
	}
}

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestFetchRejectsRedirectsAndNeverSendsCredentials(t *testing.T) {
	f := New()
	calls := 0
	f.client.Transport = testTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatal("credentials forwarded")
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://other.example/receipt"}}, Body: io.NopCloser(strings.NewReader("redirect")), Request: r}, nil
	})
	if _, _, err := f.Fetch(context.Background(), "https://files.example/receipt?signature=temporary"); err == nil {
		t.Fatal("redirect accepted")
	}
	if calls != 1 {
		t.Fatalf("followed redirect with %d requests", calls)
	}
}
func TestFetchEnforcesStreamingLimitAndSniffsBytes(t *testing.T) {
	f := New()
	f.client.Transport = testTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/png"}}, ContentLength: -1, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 33))), Request: r}, nil
	})
	if _, _, err := f.fetch(context.Background(), "https://files.example/file", 32); err == nil {
		t.Fatal("stream exceeded bound")
	}
	f.client.Transport = testTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/png"}}, ContentLength: -1, Body: io.NopCloser(strings.NewReader("<script>alert(1)</script>")), Request: r}, nil
	})
	_, mime, err := f.fetch(context.Background(), "https://files.example/file", 64)
	if err != nil {
		t.Fatal(err)
	}
	if mime == "image/png" {
		t.Fatal("trusted external MIME label")
	}
}
