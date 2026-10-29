package monitor

import (
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var (
	networkLogURL           = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s<>"'\x60]+`)
	networkLogQuery         = regexp.MustCompile(`[?#][^\s<>"'\x60]*`)
	networkLogHeader        = regexp.MustCompile(`(?im)\b(?:proxy-authorization|authorization|set-cookie|cookie)["']?\s*[:=]\s*[^\r\n]*`)
	networkLogCredential    = regexp.MustCompile(`(?i)\b(?:access[_-]?token|refresh[_-]?token|id[_-]?token|token|secret|api[_-]?key|x-api-key|password|passwd|credential|client[_-]?secret)["']?\s*[:=]\s*(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;\]}]+)`)
	networkLogAuth          = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[a-z0-9._~+/=-]+`)
	networkLogSecretPattern = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{8,}|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)`)
)

// Keep operational evidence (destination, rule, selected node, DNS answer and
// errors). Redact before truncating so a cut-off secret cannot evade matching.
// Only this bounded projection enters the ring buffer or persistent storage.
func sanitizeNetworkLog(raw string, secrets []string) string {
	raw = networkLogURL.ReplaceAllStringFunc(raw, func(value string) string {
		u, err := url.Parse(value)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ws" && u.Scheme != "wss") {
			return "[redacted-url]"
		}
		u.User = nil
		u.RawQuery, u.Fragment, u.RawFragment = "", "", ""
		u.ForceQuery = false
		return u.String()
	})
	raw = networkLogQuery.ReplaceAllString(raw, "[redacted-query]")
	raw = networkLogHeader.ReplaceAllString(raw, "[redacted-header]")
	raw = networkLogCredential.ReplaceAllString(raw, "[redacted-credential]")
	raw = networkLogAuth.ReplaceAllString(raw, "[redacted-authorization]")
	raw = networkLogSecretPattern.ReplaceAllString(raw, "[redacted-key]")
	// Replace known secrets after structured fields: inserting marker brackets
	// into userinfo first would break URL matching before it reaches the host.
	variants := []string{}
	for _, secret := range secrets {
		if secret != "" {
			encoded, _ := json.Marshal(secret)
			variants = append(variants, secret, url.QueryEscape(secret), url.PathEscape(secret), string(encoded[1:len(encoded)-1]))
		}
	}
	sort.Slice(variants, func(i, j int) bool { return len(variants[i]) > len(variants[j]) })
	for _, secret := range variants {
		raw = strings.ReplaceAll(raw, secret, "[redacted]")
	}
	raw = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, raw)
	runes := []rune(strings.TrimSpace(raw))
	if len(runes) > 2048 {
		return string(runes[:2047]) + "…"
	}
	if len(runes) == 0 {
		return "核心日志事件（无正文）。"
	}
	return string(runes)
}
