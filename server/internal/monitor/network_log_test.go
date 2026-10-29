package monitor

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNetworkLogPreservesDiagnosticEvidence(t *testing.T) {
	for _, raw := range []string{
		"[TCP] --> pipio.io:443 match Match using 星网-Starnetwork[🇸🇬新加坡1]",
		"[DNS] cache hit fgg.zlstar.sbs --> [52.194.236.163] A",
		"[TCP] dial 新加坡1 --> pipio.io:443 error: connection refused",
		"dial 台湾1 --> example.com:443 i/o timeout",
	} {
		if got := sanitizeNetworkLog(raw, nil); got != raw {
			t.Fatalf("diagnostic evidence lost: %q -> %q", raw, got)
		}
	}
}

func TestNetworkLogRedactsCredentialsBeforeBuffering(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		secrets []string
		hidden  []string
		keep    string
	}{
		{raw: "GET https://user:password@pipio.io/path?api_key=private-value#fragment", hidden: []string{"user", "password", "private-value", "fragment"}, keep: "https://pipio.io/path"},
		{raw: "GET https://private-user:core-secret@pipio.io/path?value=hidden", secrets: []string{"core-secret"}, hidden: []string{"private-user", "core-secret", "hidden"}, keep: "https://pipio.io/path"},
		{raw: "GET /path?arbitrary=private-query using 台湾1", hidden: []string{"private-query"}, keep: "台湾1"},
		{raw: "GET /path?tokens[]=private-array using 台湾1", hidden: []string{"private-array"}, keep: "台湾1"},
		{raw: "GET https://user:pass@[::1]:9090/path?private=x", hidden: []string{"user", "pass", "private=x"}, keep: "https://[::1]:9090/path"},
		{raw: "[TCP] pipio.io:443\nAuthorization: Bearer xyz-private\nCookie: session=abc-private", hidden: []string{"xyz-private", "abc-private"}, keep: "pipio.io:443"},
		{raw: `error node 台湾1 {"token":"json-secret", "api_key": "second-secret", "password":"two words"}`, hidden: []string{"json-secret", "second-secret", "two words"}, keep: "台湾1"},
		{raw: "error 台湾1 token=plain-secret; client_secret='quoted secret'", hidden: []string{"plain-secret", "quoted secret"}, keep: "台湾1"},
		{raw: "台湾1 bearer bearer-private Basic YWJjZGVmZw== sk-abcdefgh123456", hidden: []string{"bearer-private", "YWJjZGVmZw==", "sk-abcdefgh123456"}, keep: "台湾1"},
		{raw: "台湾1 core-secret core-secret-long abc%2B123", secrets: []string{"core-secret", "core-secret-long", "abc+123"}, hidden: []string{"core-secret", "long", "abc%2B123"}, keep: "台湾1"},
		{raw: "台湾1 ss://dXNlcjpwYXNz@server.example:443", hidden: []string{"dXNlcjpwYXNz"}, keep: "台湾1"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got := sanitizeNetworkLog(tc.raw, tc.secrets)
			for _, hidden := range tc.hidden {
				if strings.Contains(got, hidden) {
					t.Fatalf("credential %q leaked in %q", hidden, got)
				}
			}
			if !strings.Contains(got, tc.keep) {
				t.Fatalf("diagnostic evidence lost: %q", got)
			}
		})
	}
	got := sanitizeNetworkLog(strings.Repeat("节", 3000)+" token=never-expose", nil)
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) != 2048 || strings.Contains(got, "never-expose") {
		t.Fatal("invalid size/encoding boundary")
	}
	if got := sanitizeNetworkLog("node\x1b\x00\u202e error", nil); strings.ContainsAny(got, "\x1b\x00\u202e") {
		t.Fatal("terminal/control characters preserved")
	}
}
