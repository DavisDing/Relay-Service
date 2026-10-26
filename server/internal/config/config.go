// Package config validates deployment configuration without logging secrets.
package config

import (
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr     string
	PublicURL      string
	DatabaseURL    string
	EncryptionKey  []byte
	BootstrapToken string
	SecureCookies  bool
	LogLevel       string
}

func Load() (Config, error) { return LoadWith(os.Getenv) }

// DatabaseURLFromEnvironment is also used by the isolated migration command,
// which must not require web, cookie, or encryption settings.
func DatabaseURLFromEnvironment() string { return os.Getenv("RELAY_DATABASE_URL") }

func LoadWith(getenv func(string) string) (Config, error) {
	get := func(key, defaultValue string) string {
		value := strings.TrimSpace(getenv(key))
		if value == "" {
			return defaultValue
		}
		return value
	}
	c := Config{ListenAddr: get("RELAY_LISTEN_ADDR", ":8787"), PublicURL: get("RELAY_PUBLIC_URL", "http://localhost:8787"), DatabaseURL: getenv("RELAY_DATABASE_URL"), BootstrapToken: getenv("RELAY_BOOTSTRAP_TOKEN"), LogLevel: get("RELAY_LOG_LEVEL", "info")}
	_, listenPort, err := net.SplitHostPort(c.ListenAddr)
	portNumber, portErr := strconv.Atoi(listenPort)
	if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 {
		return c, fmt.Errorf("RELAY_LISTEN_ADDR must be a host:port")
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
		return c, fmt.Errorf("RELAY_PUBLIC_URL must be an HTTP(S) origin without path or credentials")
	}
	c.PublicURL = u.Scheme + "://" + u.Host
	c.SecureCookies = u.Scheme == "https"
	if raw := strings.TrimSpace(getenv("RELAY_SECURE_COOKIES")); raw != "" {
		secure, err := strconv.ParseBool(raw)
		if err != nil {
			return c, fmt.Errorf("RELAY_SECURE_COOKIES must be true or false")
		}
		c.SecureCookies = secure
	}
	if c.SecureCookies != (u.Scheme == "https") {
		return c, fmt.Errorf("RELAY_SECURE_COOKIES must match the public URL scheme")
	}
	if err := ValidateDatabaseURL(c.DatabaseURL); err != nil {
		return c, err
	}
	key, err := hex.DecodeString(getenv("RELAY_ENCRYPTION_KEY"))
	if err != nil || len(key) != 32 {
		return c, fmt.Errorf("RELAY_ENCRYPTION_KEY must contain 64 hexadecimal characters")
	}
	if strings.Trim(c.BootstrapToken, " ") == "" {
		c.BootstrapToken = ""
	} else if len(c.BootstrapToken) < 32 || strings.Contains(c.BootstrapToken, "replace-with") {
		return c, fmt.Errorf("RELAY_BOOTSTRAP_TOKEN must be a random token of at least 32 bytes")
	}
	c.EncryptionKey = key
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return c, fmt.Errorf("RELAY_LOG_LEVEL must be debug, info, warn or error")
	}
	return c, nil
}

func ValidateDatabaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.Path == "" || u.Path == "/" || strings.Contains(u.Path[1:], "/") || u.Fragment != "" || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return fmt.Errorf("RELAY_DATABASE_URL must be a PostgreSQL URL with an explicit database")
	}
	if u.Path == "/postgres" || u.Path == "/template0" || u.Path == "/template1" {
		return fmt.Errorf("RELAY_DATABASE_URL must use a dedicated Relay database")
	}
	return nil
}
