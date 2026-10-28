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
	details                *Details
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
	case KindMihomo:
		return "/providers/proxies"
	case KindOpenAI:
		return "/v1/models"
	case KindStatuspage:
		return "/api/v2/summary.json"
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
func (e *Engine) executeBasic(ctx context.Context, rec serviceRecord) observation {
	s := rec.Service
	u, err := validatedURL(s.BaseURL)
	if err != nil {
		return failure(StatusUnsupported, "configuration_invalid", "服务地址无效，请仅填写协议、主机和端口。")
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
		return positive("connectivity_only", "TCP 连接成功；应用就绪状态和推理能力未测试。")
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
		return failure(StatusUnsupported, "configuration_invalid", "服务地址无效，请仅填写协议、主机和端口。")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Relay-Service/monitor-v1")
	credential := ""
	if rec.Cipher != "" {
		if e.crypt == nil {
			return failure(StatusUnauthorized, "credential_unavailable", "检查凭据不可用，请重新配置。")
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
			return failure(StatusUnauthorized, "credential_unavailable", "检查凭据无法解密，请重新配置。")
		}
		if strings.ContainsAny(token, "\r\n") {
			return failure(StatusUnauthorized, "credential_invalid", "检查凭据格式无效，请重新配置。")
		}
		credential = token
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return networkFailure(ctx, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return failure(StatusUnauthorized, "authentication_failed", "检查凭据被拒绝，请核对凭据和权限。")
	case resp.StatusCode >= 300 && resp.StatusCode <= 399:
		return failure(StatusUnsupported, "redirect_rejected", "检查接口发生重定向，请核对地址和认证方式。")
	case resp.StatusCode == 404 || resp.StatusCode == 405:
		return failure(StatusUnsupported, "endpoint_unsupported", "当前服务未提供此检查接口，请核对服务类型与版本。")
	case resp.StatusCode == 429:
		return failure(StatusUnhealthy, "rate_limited", "检查被服务限流，请稍后重试。")
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return failure(StatusUnhealthy, "http_error", "检查接口返回错误状态，请检查上游服务。")
	}
	limit := int64(65536)
	if s.Kind == KindMihomo || s.Kind == KindOpenAI || s.Kind == KindStatuspage {
		limit = 1 << 20
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return failure(StatusUnhealthy, "response_incomplete", "检查响应不完整，请重试。")
	}
	if int64(len(b)) > limit {
		return failure(StatusUnsupported, "response_too_large", "检查响应超过大小限制。")
	}
	contentType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	trim := strings.TrimSpace(string(b))
	if contentType == "text/html" || strings.HasPrefix(strings.ToLower(trim), "<!doctype html") || strings.HasPrefix(strings.ToLower(trim), "<html") {
		return failure(StatusUnsupported, "unexpected_html", "检查接口返回了网页，请核对服务类型和 API 地址。")
	}
	if s.Kind == KindMihomo || s.Kind == KindOpenAI || s.Kind == KindStatuspage {
		result := detailObservation(s.Kind, b, e.now())
		if reflectedCredential(result.details, credential) {
			return failure(StatusUnsupported, "invalid_response", "监控响应包含凭据，已拒绝展示。")
		}
		return result
	}
	if s.Kind == KindHTTP {
		return positive("connectivity_only", "HTTP 地址响应成功；应用就绪状态和推理能力未测试。")
	}
	// LiteLLM liveliness is a JSON string, not the model-invoking /health API.
	// Accept only its documented process response, never arbitrary 2xx text.
	if s.Kind == KindLiteLLM {
		var text string
		if json.Unmarshal(b, &text) == nil && text == "I\u0027m alive!" {
			return positive("process", "进程存活接口响应成功；网关就绪状态和推理能力未测试。")
		}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil || obj == nil {
		return failure(StatusUnsupported, "invalid_response", "检查接口未返回支持的 JSON 对象。")
	}
	if s.Kind == KindOllama {
		var v string
		if json.Unmarshal(obj["version"], &v) != nil || strings.TrimSpace(v) == "" || len(v) > 128 {
			return failure(StatusUnsupported, "invalid_response", "版本接口返回的数据格式不符合预期。")
		}
		return positive("process", "版本接口响应成功；模型和推理能力未测试。")
	}
	for _, key := range []string{"status", "healthy", "ok"} {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		var boolean bool
		if json.Unmarshal(raw, &boolean) == nil {
			if boolean {
				return positive("process", "进程健康检查成功；推理能力未测试。")
			}
			return failure(StatusUnhealthy, "health_failed", "服务健康接口报告失败。")
		}
		var v string
		if json.Unmarshal(raw, &v) == nil {
			switch strings.ToLower(v) {
			case "ok", "healthy", "alive", "up", "ready", "success", "running":
				return positive("process", "进程健康检查成功；推理能力未测试。")
			case "error", "failed", "failure", "unhealthy", "down", "not_ready":
				return failure(StatusUnhealthy, "health_failed", "服务健康接口报告失败。")
			}
		}
	}
	return failure(StatusUnsupported, "unverified_response", "无法判断当前安装的检查响应，请核对接口合同。")
}
func networkFailure(ctx context.Context, err error) observation {
	if ctx.Err() != nil {
		return failure(StatusUnhealthy, "timeout", "检查超时或已取消，请检查网络和超时设置。")
	}
	if strings.Contains(err.Error(), "destination is not permitted") {
		return failure(StatusUnsupported, "target_blocked", "目标需要私网授权，或属于禁止访问的元数据地址。")
	}
	var op *net.OpError
	if errors.As(err, &op) {
		if op.Timeout() {
			return failure(StatusUnhealthy, "timeout", "检查超时，请检查网络和超时设置。")
		}
		return failure(StatusUnhealthy, "network_error", "无法连接检查接口，请检查地址、DNS 和网络。")
	}
	var uerr *url.Error
	if errors.As(err, &uerr) && uerr.Timeout() {
		return failure(StatusUnhealthy, "timeout", "检查超时，请检查网络和超时设置。")
	}
	return failure(StatusUnhealthy, "network_error", "无法连接检查接口，请检查地址、DNS 和网络。")
}
