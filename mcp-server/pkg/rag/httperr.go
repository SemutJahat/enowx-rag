package rag

import (
	"bytes"
	"fmt"
	"net/http"
	"runtime"
	"strings"
)

// UserAgent identifies enowx-rag's HTTP clients (client mode, connect, push)
// to the server and to any proxy in front of it. Some WAF and bot rules block
// Go's default "Go-http-client/1.1", and a named agent is easy to allow.
var UserAgent = userAgent("dev")

// SetClientVersion puts the build version into UserAgent.
func SetClientVersion(v string) {
	if v = strings.TrimSpace(v); v != "" {
		UserAgent = userAgent(v)
	}
}

func userAgent(v string) string {
	return fmt.Sprintf("enowx-rag/%s (%s/%s)", v, runtime.GOOS, runtime.GOARCH)
}

// ProxyProblem explains an HTTP error that came from a proxy or CDN in front
// of the enowx-rag server (Cloudflare, Traefik, Caddy, nginx) rather than from
// enowx-rag itself, whose errors are always JSON. It returns "" for
// enowx-rag's own answers and for successes.
//
// The point is to never report "the token was rejected" when the request did
// not even reach enowx-rag: a Cloudflare WAF block is a 403 too.
func ProxyProblem(status int, h http.Header, body []byte) string {
	if status < 400 || LooksJSON(h, body) {
		return ""
	}
	cf := h.Get("Cf-Ray") != "" || strings.EqualFold(strings.TrimSpace(h.Get("Server")), "cloudflare")
	who, ray := "a proxy in front of enowx-rag", ""
	if cf {
		who = "Cloudflare"
		if r := h.Get("Cf-Ray"); r != "" {
			ray = " (Ray ID " + strings.SplitN(r, "-", 2)[0] + ")"
		}
	}
	if cf {
		if why, ok := cloudflareOrigin[status]; ok {
			return fmt.Sprintf("Cloudflare could not get an answer from the server (HTTP %d)%s: %s", status, ray, why)
		}
	}
	switch status {
	case http.StatusRequestEntityTooLarge:
		limit := ""
		if cf {
			limit = " Cloudflare's Free and Pro plans accept at most 100 MB per request."
		}
		return fmt.Sprintf("%s refused the request as too large (HTTP 413)%s.%s", who, ray, limit)
	case http.StatusForbidden:
		return fmt.Sprintf("%s blocked the request before it reached enowx-rag (HTTP 403)%s. This is not about the token: check the WAF/firewall rules, IP allowlists and bot protection for this hostname (Cloudflare: Security → Events shows the rule; Bot Fight Mode blocks API clients)", who, ray)
	case http.StatusUnauthorized:
		return fmt.Sprintf("%s asked for its own login (HTTP 401)%s, so the request never reached enowx-rag: exempt /api and /mcp from the proxy's authentication", who, ray)
	case http.StatusTooManyRequests:
		return fmt.Sprintf("%s rate-limited the request (HTTP 429)%s: wait a moment, or relax the rate-limiting rule for this hostname", who, ray)
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return fmt.Sprintf("%s got no answer from enowx-rag (HTTP %d)%s: the container is down, restarting or still starting, or the proxy points at the wrong service or port (enowx-rag listens on 7777)", who, status, ray)
	case http.StatusNotFound:
		return fmt.Sprintf("%s answered 404 with a page that is not enowx-rag's%s: the domain routes to something else (check the domain's service and port 7777 in the proxy)", who, ray)
	}
	return fmt.Sprintf("%s answered HTTP %d%s with a web page instead of enowx-rag's JSON", who, status, ray)
}

// cloudflareOrigin explains Cloudflare's own 52x/530 errors: Cloudflare is
// fine, the hop from Cloudflare to the server is not.
var cloudflareOrigin = map[int]string{
	520: "the server sent an empty or invalid answer (the reverse proxy on the server crashed or reset the connection)",
	521: "the server refused the connection (the reverse proxy on the server, e.g. Traefik, is not listening on 443/80)",
	522: "the connection to the server timed out (a firewall such as the cloud provider's security list or ufw is dropping Cloudflare's IPs on 443/80)",
	523: "the server is unreachable (the DNS record points at a wrong IP)",
	524: "the server took longer than 100 seconds to answer",
	525: "the TLS handshake with the server failed (with SSL mode Full or Full (strict) the server must serve HTTPS on 443)",
	526: "the server's certificate is not valid for Full (strict) (install a Cloudflare Origin Certificate covering this hostname, or a public certificate)",
	530: "Cloudflare cannot resolve or reach the server (often error 1016: the DNS record is missing)",
}

// LooksJSON reports whether an HTTP body is JSON (by Content-Type or shape).
func LooksJSON(h http.Header, body []byte) bool {
	if strings.Contains(strings.ToLower(h.Get("Content-Type")), "json") {
		return true
	}
	b := bytes.TrimSpace(body)
	return len(b) > 0 && (b[0] == '{' || b[0] == '[')
}

// NotJSONProblem explains a successful answer that is a web page where
// enowx-rag would send JSON: a login page in front of the server (Cloudflare
// Access, an SSO proxy) or a URL that points at another site.
func NotJSONProblem(h http.Header, body []byte) string {
	if len(bytes.TrimSpace(body)) == 0 || LooksJSON(h, body) {
		return ""
	}
	return "the server answered with a web page instead of enowx-rag's JSON: the URL points at another site, or a login page (Cloudflare Access, an SSO proxy) sits in front of it — exempt /api and /mcp from it"
}
