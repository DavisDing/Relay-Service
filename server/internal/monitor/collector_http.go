package monitor

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// FetchError only carries stable, localized diagnostics, never upstream bodies.
type FetchError struct{ Status, Class, Message string }

func (e *FetchError) Error() string { return e.Class }
func fetchFailure(err error) observation {
	if errors.Is(err, ErrPublicStatusConfig) {
		return failure(StatusUnsupported, "configuration_invalid", "公开状态页解析规则无效，请核对字段、组件匹配和状态值。")
	}
	var f *FetchError
	if errors.As(err, &f) {
		return failure(f.Status, f.Class, f.Message)
	}
	return failure(StatusUnsupported, "invalid_response", "监控接口未返回支持的数据结构。")
}
func (e *Engine) collector(ctx context.Context, rec serviceRecord) (func(context.Context, string) ([]byte, error), *string, func(), error) {
	s := rec.Service
	origin, err := validatedURL(s.BaseURL)
	if err != nil {
		return nil, nil, nil, &FetchError{StatusUnsupported, "configuration_invalid", "服务地址无效，请仅填写协议、主机和端口。"}
	}
	token := ""
	// Public status sources never receive saved monitoring credentials.
	if rec.Cipher != "" && s.Kind != KindStatuspage {
		if e.crypt == nil {
			return nil, nil, nil, &FetchError{StatusUnauthorized, "credential_unavailable", "检查凭据不可用，请重新配置。"}
		}
		if scoped, ok := e.crypt.(interface {
			DecryptFor(string, string, string) (string, error)
		}); ok {
			token, err = scoped.DecryptFor("monitor.service", s.ID, rec.Cipher)
		} else {
			token, err = e.crypt.Decrypt(rec.Cipher)
		}
		if err != nil || strings.ContainsAny(token, "\r\n") {
			return nil, nil, nil, &FetchError{StatusUnauthorized, "credential_invalid", "检查凭据无法解密或格式无效，请重新配置。"}
		}
	}
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return safeDial(ctx, network, address, s.AllowPrivate)
	}, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: time.Duration(s.TimeoutSeconds) * time.Second, DisableKeepAlives: true, ForceAttemptHTTP2: true}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	calls, total := 0, int64(0)
	fetch := func(ctx context.Context, path string) ([]byte, error) {
		relative, err := url.Parse(path)
		if err != nil || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || relative.IsAbs() || relative.Host != "" || relative.User != nil || relative.Fragment != "" || len(path) > 2048 {
			return nil, &FetchError{StatusUnsupported, "configuration_invalid", "采集接口路径无效。"}
		}
		calls++
		if calls > 8 {
			return nil, &FetchError{StatusUnsupported, "response_too_large", "采集请求数超过限制。"}
		}
		u := *origin
		u.Path = relative.Path
		u.RawPath = relative.RawPath
		u.RawQuery = relative.RawQuery
		u.Fragment = ""
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, &FetchError{StatusUnsupported, "configuration_invalid", "采集接口路径无效。"}
		}
		req.Header.Set("Accept", "application/json, text/html;q=0.8")
		req.Header.Set("User-Agent", "Relay-Service/monitor-v2")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			o := networkFailure(ctx, err)
			return nil, &FetchError{o.status, o.class, o.message}
		}
		defer resp.Body.Close()
		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			return nil, &FetchError{StatusUnauthorized, "authentication_failed", "采集凭据或权限不足。"}
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			return nil, &FetchError{StatusUnsupported, "redirect_rejected", "采集接口发生重定向，请核对地址。"}
		case resp.StatusCode == 404 || resp.StatusCode == 405:
			return nil, &FetchError{StatusUnsupported, "endpoint_unsupported", "当前安装未提供此采集接口。"}
		case resp.StatusCode == 429:
			return nil, &FetchError{StatusUnhealthy, "rate_limited", "采集被服务限流，请稍后重试。"}
		case resp.StatusCode < 200 || resp.StatusCode >= 300:
			return nil, &FetchError{StatusUnhealthy, "http_error", "采集接口返回错误状态。"}
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		if err != nil {
			return nil, &FetchError{StatusUnhealthy, "response_incomplete", "采集响应不完整。"}
		}
		total += int64(len(b))
		if len(b) > 1<<20 || total > 5<<20 {
			return nil, &FetchError{StatusUnsupported, "response_too_large", "采集响应超过大小限制。"}
		}
		return b, nil
	}
	return fetch, &token, transport.CloseIdleConnections, nil
}

func (e *Engine) execute(ctx context.Context, rec serviceRecord) observation {
	s := rec.Service
	if s.Kind != KindMihomo && s.Kind != KindStatuspage && !s.CollectModels && !s.CollectLogs {
		return e.executeBasic(ctx, rec)
	}
	seconds := s.TimeoutSeconds
	if seconds <= 0 {
		seconds = 5
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	fetch, token, closeClient, err := e.collector(ctx, rec)
	if err != nil {
		return fetchFailure(err)
	}
	defer closeClient()
	var result observation
	switch s.Kind {
	case KindMihomo:
		d, err := CollectMihomo(ctx, fetch, e.now())
		if err != nil {
			return fetchFailure(err)
		}
		result = positive("subscription_metadata", "已读取订阅额度和已有节点质量记录；未执行主动测速。")
		if d.SubscriptionState == "not_exposed" {
			result.message = "核心接口可达；仅返回组或内联视图，机场订阅及额度未知；已有节点质量独立展示，未主动测速。"
		}
		result.details = &Details{Source: "/providers/proxies", Mihomo: d, Count: d.SubscriptionCount, Truncated: d.Truncated}
		for _, sub := range d.Subscriptions {
			result.details.Subscriptions = append(result.details.Subscriptions, Subscription{Name: sub.Name, Info: sub.Info})
		}
		if d.SubscriptionUnavailable {
			result.success = false
			result.status = StatusUnhealthy
			result.class = "subscription_unavailable"
			result.message = "订阅已到期或流量已用尽，请检查订阅信息。"
		}

	case KindStatuspage:
		cfg := s.PublicStatus
		path := "/api/v2/summary.json"
		format := "auto"
		if cfg != nil {
			if cfg.ResourcePath != "" {
				path = cfg.ResourcePath
			} else if cfg.Format == "pipio" {
				path = "/api/uptime/status"
			}
			format = cfg.Format
		}
		b, err := fetch(ctx, path)
		if err != nil {
			return fetchFailure(err)
		}
		d, err := ParsePublicStatus(format, b, "", cfg, e.now())
		if err != nil {
			return fetchFailure(err)
		}
		d.Source = path
		result = positive("provider_reported", "已读取服务商公开状态或站方探针，详见各组件状态。")
		if d.Unhealthy {
			result = failure(StatusUnhealthy, "provider_reported", "服务商公开状态或站方探针报告异常，详见组件证据。")
		} else if d.Unknown {
			result = failure(StatusUnsupported, "unverified_response", "公开状态包含未知或不足以判断的证据。")
		}
		result.details = &Details{Source: path, PublicStatus: d, Count: d.Count, Truncated: d.Truncated}
	default:
		result = e.executeBasic(ctx, rec)
		d := CollectGateway(ctx, s.Kind, GatewayOptions{Models: s.CollectModels, Logs: s.CollectLogs}, fetch, e.now())
		result.details = &Details{Source: "gateway", Gateway: d}
	}
	if reflectedCredential(result.details, *token) {
		return failure(StatusUnsupported, "invalid_response", "监控响应包含凭据，已拒绝展示。")
	}
	return result
}
