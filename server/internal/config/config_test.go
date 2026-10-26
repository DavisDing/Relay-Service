package config

import (
	"strings"
	"testing"
)

func TestConfigurationRejectsInvalidSecretsAndCookieMismatch(t *testing.T) {
	settings := map[string]string{"RELAY_DATABASE_URL": "postgres://relay:password@postgres:5432/relay?sslmode=disable", "RELAY_ENCRYPTION_KEY": strings.Repeat("ab", 32), "RELAY_BOOTSTRAP_TOKEN": strings.Repeat("x", 32), "RELAY_PUBLIC_URL": "https://relay.example.test"}
	get := func(k string) string { return settings[k] }
	c, err := LoadWith(get)
	if err != nil {
		t.Fatal(err)
	}
	if !c.SecureCookies || len(c.EncryptionKey) != 32 {
		t.Fatalf("wrong config %#v", c)
	}
	settings["RELAY_SECURE_COOKIES"] = "false"
	if _, err := LoadWith(get); err == nil {
		t.Fatal("HTTPS accepted insecure cookies")
	}
	delete(settings, "RELAY_SECURE_COOKIES")
	settings["RELAY_ENCRYPTION_KEY"] = "bad"
	if _, err := LoadWith(get); err == nil {
		t.Fatal("invalid encryption key accepted")
	}
}

func TestPublicURLDoesNotAllowCredentialsOrPaths(t *testing.T) {
	for _, public := range []string{"javascript:alert(1)", "https://user:password@example.com", "https://example.com/path", "https://example.com?token=x"} {
		_, err := LoadWith(func(k string) string {
			switch k {
			case "RELAY_PUBLIC_URL":
				return public
			case "RELAY_ENCRYPTION_KEY":
				return strings.Repeat("ab", 32)
			case "RELAY_DATABASE_URL":
				return "postgres://postgres@localhost/relay"
			}
			return ""
		})
		if err == nil {
			t.Errorf("accepted %s", public)
		}
	}
}
