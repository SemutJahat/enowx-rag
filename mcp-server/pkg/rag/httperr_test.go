package rag

import (
	"net/http"
	"strings"
	"testing"
)

func TestProxyProblem(t *testing.T) {
	cf := http.Header{"Server": {"cloudflare"}, "Cf-Ray": {"8f1c2d3e4f5a6b7c-SIN"}, "Content-Type": {"text/html"}}
	page := []byte("<!DOCTYPE html><html><title>Attention Required! | Cloudflare</title></html>")
	jsonH := http.Header{"Content-Type": {"application/json"}}

	cases := []struct {
		name   string
		status int
		h      http.Header
		body   []byte
		want   []string // all must appear; nil = no proxy problem
		forbid string
	}{
		{"enowx-rag 401 is not a proxy", 401, jsonH, []byte(`{"error":"unauthorized"}`), nil, ""},
		{"enowx-rag 403 is not a proxy", 403, jsonH, []byte(`{"error":"setup is restricted"}`), nil, ""},
		{"JSON without header", 500, http.Header{}, []byte(` {"error":"x"}`), nil, ""},
		{"success", 200, cf, page, nil, ""},
		{"cloudflare WAF block", 403, cf, page, []string{"Cloudflare blocked", "not about the token", "Ray ID 8f1c2d3e4f5a6b7c"}, "rejected the token"},
		{"cloudflare body limit", 413, cf, page, []string{"too large", "100 MB"}, ""},
		{"other proxy body limit", 413, http.Header{"Server": {"nginx"}}, []byte("<html>413</html>"), []string{"a proxy in front of enowx-rag", "too large"}, "100 MB"},
		{"origin timeout", 522, cf, page, []string{"HTTP 522", "firewall", "Cloudflare's IPs"}, ""},
		{"origin cert", 526, cf, page, []string{"Full (strict)", "Origin Certificate"}, ""},
		{"traefik 404", 404, http.Header{"Content-Type": {"text/plain"}}, []byte("404 page not found"), []string{"not enowx-rag's", "7777"}, ""},
		{"bad gateway", 502, http.Header{}, []byte("Bad Gateway"), []string{"no answer from enowx-rag", "7777"}, ""},
		{"empty 403", 403, http.Header{}, nil, []string{"blocked the request before it reached enowx-rag"}, ""},
	}
	for _, c := range cases {
		got := ProxyProblem(c.status, c.h, c.body)
		if c.want == nil {
			if got != "" {
				t.Errorf("%s: got %q, want none", c.name, got)
			}
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q lacks %q", c.name, got, w)
			}
		}
		if c.forbid != "" && strings.Contains(got, c.forbid) {
			t.Errorf("%s: %q must not say %q", c.name, got, c.forbid)
		}
	}
}

func TestNotJSONProblem(t *testing.T) {
	if NotJSONProblem(http.Header{"Content-Type": {"application/json"}}, []byte(`{}`)) != "" {
		t.Error("JSON flagged")
	}
	if NotJSONProblem(http.Header{}, nil) != "" {
		t.Error("empty body flagged")
	}
	if !strings.Contains(NotJSONProblem(http.Header{"Content-Type": {"text/html"}}, []byte("<html>Sign in</html>")), "login page") {
		t.Error("HTML login page not explained")
	}
}

func TestUserAgent(t *testing.T) {
	old := UserAgent
	t.Cleanup(func() { UserAgent = old })
	SetClientVersion("v1.2.3")
	if !strings.HasPrefix(UserAgent, "enowx-rag/v1.2.3 (") {
		t.Errorf("UserAgent = %q", UserAgent)
	}
	SetClientVersion("  ")
	if !strings.HasPrefix(UserAgent, "enowx-rag/v1.2.3 (") {
		t.Errorf("blank version changed UserAgent to %q", UserAgent)
	}
}
