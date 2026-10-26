package monitor

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

type observation struct {
	success                bool
	status, class, message string
}

func failure(status, class, msg string) observation {
	return observation{status: status, class: class, message: msg}
}
func positive(class, msg string) observation {
	return observation{success: true, status: StatusHealthy, class: class, message: msg}
}

var blockedMetadata = []netip.Prefix{netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("100.100.100.200/32"), netip.MustParsePrefix("fd00:ec2::254/128")}

func permittedIP(ip netip.Addr, allowPrivate bool) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	for _, p := range blockedMetadata {
		if p.Contains(ip) {
			return false
		}
	}
	if ip.IsLoopback() || ip.IsPrivate() {
		return allowPrivate
	}
	if !ip.IsGlobalUnicast() {
		return false
	}
	if netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
		return allowPrivate
	}
	return true
}
func safeDial(ctx context.Context, network, address string, allowPrivate bool) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid destination")
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("name resolution failed")
	}
	for _, ip := range ips {
		if !permittedIP(ip, allowPrivate) {
			return nil, errors.New("destination is not permitted")
		}
	}
	d := net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}
	var last error
	for _, ip := range ips {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}
func endpoint(kind string) string {
	switch kind {
	case KindLiteLLM:
		return "/health/liveliness"
	case KindHermes, KindWorkbuddy2API, KindOpenWebUI:
		return "/health"
	case KindOllama:
		return "/api/version"
	case KindMetaCubeXD:
		return "/api/control/health"
	default:
		return "/"
	}
}
func (e *Engine) execute(ctx context.Context, rec serviceRecord) observation {
	s := rec.Service
	u, err := validatedURL(s.BaseURL)
	if err != nil {
		return failure(StatusUnsupported, "configuration_invalid", "Connection origin is invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutSeconds)*time.Second)
	defer cancel()
	if s.Kind == KindTCP {
		port := u.Port()
		if port == "" {
			if u.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		conn, err := safeDial(ctx, "tcp", net.JoinHostPort(u.Hostname(), port), s.AllowPrivate)
		if err != nil {
			return networkFailure(ctx, err)
		}
		conn.Close()
		return positive("connectivity_only", "TCP connection succeeded; application readiness and inference are untested")
	}
	u.Path = endpoint(s.Kind)
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return safeDial(ctx, network, address, s.AllowPrivate)
	}, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: time.Duration(s.TimeoutSeconds) * time.Second, MaxIdleConns: 1, MaxIdleConnsPerHost: 1, DisableKeepAlives: true, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Duration(s.TimeoutSeconds) * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return failure(StatusUnsupported, "configuration_invalid", "Connection origin is invalid")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Relay-Service/monitor-v1")
	if rec.Cipher != "" {
		if e.crypt == nil {
			return failure(StatusUnauthorized, "credential_unavailable", "Monitoring credential is unavailable")
		}
		var token string
		var err error
		if scoped, ok := e.crypt.(interface {
			DecryptFor(string, string, string) (string, error)
		}); ok {
			token, err = scoped.DecryptFor("monitor.service", s.ID, rec.Cipher)
		} else {
			token, err = e.crypt.Decrypt(rec.Cipher)
		}
		if err != nil {
			return failure(StatusUnauthorized, "credential_unavailable", "Monitoring credential could not be decrypted")
		}
		if strings.ContainsAny(token, "\r\n") {
			return failure(StatusUnauthorized, "credential_invalid", "Monitoring credential is invalid")
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return networkFailure(ctx, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return failure(StatusUnauthorized, "authentication_failed", "Monitoring credential was rejected")
	case resp.StatusCode >= 300 && resp.StatusCode <= 399:
		return failure(StatusUnsupported, "redirect_rejected", "Health endpoint redirected; target and authentication need verification")
	case resp.StatusCode == 404 || resp.StatusCode == 405:
		return failure(StatusUnsupported, "endpoint_unsupported", "Health endpoint is not supported by this installation")
	case resp.StatusCode == 429:
		return failure(StatusUnhealthy, "rate_limited", "Health endpoint rate limited the check")
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return failure(StatusUnhealthy, "http_error", "Health endpoint returned an unsuccessful status")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil {
		return failure(StatusUnhealthy, "response_incomplete", "Health response was incomplete")
	}
	if len(b) > 65536 {
		return failure(StatusUnsupported, "response_too_large", "Health response exceeded the size limit")
	}
	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	trim := strings.TrimSpace(string(b))
	if contentType == "text/html" || strings.HasPrefix(strings.ToLower(trim), "<!doctype html") || strings.HasPrefix(strings.ToLower(trim), "<html") {
		return failure(StatusUnsupported, "unexpected_html", "Health endpoint returned HTML rather than a health response")
	}
	if s.Kind == KindHTTP {
		return positive("connectivity_only", "HTTP origin responded successfully; application readiness and inference are untested")
	}
	// LiteLLM liveliness is a JSON string, not the model-invoking /health API.
	// Accept only its documented process response, never arbitrary 2xx text.
	if s.Kind == KindLiteLLM {
		var text string
		if json.Unmarshal(b, &text) == nil && text == "I\u0027m alive!" {
			return positive("process", "Liveliness endpoint responded; gateway readiness and inference are untested")
		}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil || obj == nil {
		return failure(StatusUnsupported, "invalid_response", "Health endpoint did not return a supported JSON object")
	}
	if s.Kind == KindOllama {
		var v string
		if json.Unmarshal(obj["version"], &v) != nil || strings.TrimSpace(v) == "" || len(v) > 128 {
			return failure(StatusUnsupported, "invalid_response", "Version endpoint did not return the expected schema")
		}
		return positive("process", "Version endpoint responded; models and inference are untested")
	}
	for _, key := range []string{"status", "healthy", "ok"} {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		var boolean bool
		if json.Unmarshal(raw, &boolean) == nil {
			if boolean {
				return positive("process", "Process health endpoint reported success; inference is untested")
			}
			return failure(StatusUnhealthy, "health_failed", "Health endpoint reported failure")
		}
		var v string
		if json.Unmarshal(raw, &v) == nil {
			switch strings.ToLower(v) {
			case "ok", "healthy", "alive", "up", "ready", "success", "running":
				return positive("process", "Process health endpoint reported success; inference is untested")
			case "error", "failed", "failure", "unhealthy", "down", "not_ready":
				return failure(StatusUnhealthy, "health_failed", "Health endpoint reported failure")
			}
		}
	}
	return failure(StatusUnsupported, "unverified_response", "Health response semantics require installation-specific verification")
}
func networkFailure(ctx context.Context, err error) observation {
	if ctx.Err() != nil {
		return failure(StatusUnhealthy, "timeout", "Health check timed out or was cancelled")
	}
	if strings.Contains(err.Error(), "destination is not permitted") {
		return failure(StatusUnsupported, "target_blocked", "Destination requires private-network consent or is a blocked metadata address")
	}
	var op *net.OpError
	if errors.As(err, &op) {
		if op.Timeout() {
			return failure(StatusUnhealthy, "timeout", "Health check timed out")
		}
		return failure(StatusUnhealthy, "network_error", "Health endpoint could not be reached")
	}
	var uerr *url.Error
	if errors.As(err, &uerr) && uerr.Timeout() {
		return failure(StatusUnhealthy, "timeout", "Health check timed out")
	}
	return failure(StatusUnhealthy, "network_error", "Health endpoint could not be reached")
}
