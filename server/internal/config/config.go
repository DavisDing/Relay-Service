// Package config validates deployment configuration without logging secrets.
package config

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr          string
	PublicURL           string
	DatabaseURL         string
	DataDir             string
	SQLitePath          string
	EncryptionKey       []byte
	BootstrapToken      string
	InitialPassword     string
	DeploymentFile      string
	InitialPasswordFile string
	SecureCookies       bool
	LogLevel            string
}

func Load() (Config, error) { return LoadWith(os.Getenv) }

// DatabaseURLFromEnvironment is also used by the isolated migration command,
// which must not require web, cookie, or encryption settings.
func DatabaseURLFromEnvironment() string { return os.Getenv("RELAY_DATABASE_URL") }

func LoadWith(getenv func(string) string) (Config, error) {
	// NAS deployments persist generated secrets separately from the database.
	// Explicit environment settings retain precedence for existing installations.
	deploymentFile := strings.TrimSpace(getenv("RELAY_DEPLOYMENT_FILE"))
	initialPasswordFile := strings.TrimSpace(getenv("RELAY_INITIAL_PASSWORD_FILE"))
	if path := deploymentFile; path != "" {
		values, err := readDeploymentFile(path)
		if err != nil {
			return Config{}, err
		}
		original := getenv
		getenv = func(name string) string {
			if value := original(name); value != "" {
				return value
			}
			return values[name]
		}
	}
	get := func(key, defaultValue string) string {
		value := strings.TrimSpace(getenv(key))
		if value == "" {
			return defaultValue
		}
		return value
	}
	c := Config{ListenAddr: get("RELAY_LISTEN_ADDR", ":8787"), PublicURL: get("RELAY_PUBLIC_URL", ""), DatabaseURL: getenv("RELAY_DATABASE_URL"), BootstrapToken: getenv("RELAY_BOOTSTRAP_TOKEN"), InitialPassword: getenv("RELAY_INITIAL_PASSWORD"), LogLevel: get("RELAY_LOG_LEVEL", "info")}
	c.DataDir = get("RELAY_DATA_DIR", "./data")
	c.SQLitePath = get("RELAY_SQLITE_PATH", filepath.Join(c.DataDir, "relay.sqlite"))
	if initialPasswordFile == "" && deploymentFile == "" {
		initialPasswordFile = filepath.Join(c.DataDir, "initial-password")
	}
	c.DeploymentFile, c.InitialPasswordFile = deploymentFile, initialPasswordFile
	if c.InitialPassword == "" && initialPasswordFile != "" {
		info, err := os.Lstat(initialPasswordFile)
		if err == nil {
			if !info.Mode().IsRegular() || info.Size() > 1024 {
				return Config{}, fmt.Errorf("invalid RELAY_INITIAL_PASSWORD_FILE; restore the original initial password file")
			}
			value, err := os.ReadFile(initialPasswordFile)
			if err != nil {
				return Config{}, fmt.Errorf("cannot read RELAY_INITIAL_PASSWORD_FILE")
			}
			c.InitialPassword = string(value)
		} else if !os.IsNotExist(err) {
			return Config{}, fmt.Errorf("cannot read RELAY_INITIAL_PASSWORD_FILE")
		}
	}
	_, listenPort, err := net.SplitHostPort(c.ListenAddr)
	portNumber, portErr := strconv.Atoi(listenPort)
	if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 {
		return c, fmt.Errorf("RELAY_LISTEN_ADDR must be a host:port")
	}
	var publicHTTPS bool
	if c.PublicURL != "" {
		u, err := url.Parse(c.PublicURL)
		if err != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
			return c, fmt.Errorf("RELAY_PUBLIC_URL must be an HTTP(S) origin without path or credentials")
		}
		c.PublicURL = u.Scheme + "://" + u.Host
		publicHTTPS = u.Scheme == "https"
	}
	c.SecureCookies = publicHTTPS
	if raw := strings.TrimSpace(getenv("RELAY_SECURE_COOKIES")); raw != "" {
		secure, err := strconv.ParseBool(raw)
		if err != nil {
			return c, fmt.Errorf("RELAY_SECURE_COOKIES must be true or false")
		}
		c.SecureCookies = secure
	}
	if c.PublicURL != "" && c.SecureCookies != publicHTTPS {
		return c, fmt.Errorf("RELAY_SECURE_COOKIES must match the public URL scheme")
	}
	if c.DatabaseURL != "" {
		if err := ValidateDatabaseURL(c.DatabaseURL); err != nil {
			return c, err
		}
	}
	key, err := hex.DecodeString(getenv("RELAY_ENCRYPTION_KEY"))
	if err != nil || (len(key) != 32 && (getenv("RELAY_ENCRYPTION_KEY") != "" || deploymentFile != "")) {
		return c, fmt.Errorf("RELAY_ENCRYPTION_KEY must contain 64 hexadecimal characters")
	}
	if strings.Trim(c.BootstrapToken, " ") == "" {
		c.BootstrapToken = ""
	} else if len(c.BootstrapToken) < 32 || strings.Contains(c.BootstrapToken, "replace-with") {
		return c, fmt.Errorf("RELAY_BOOTSTRAP_TOKEN must be a random token of at least 32 bytes")
	}
	c.EncryptionKey = key
	if c.InitialPassword != "" && (len(c.InitialPassword) < 32 || len(c.InitialPassword) > 1024 || strings.Contains(c.InitialPassword, "replace-with")) {
		return c, fmt.Errorf("RELAY_INITIAL_PASSWORD must be a unique random password of at least 32 bytes")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return c, fmt.Errorf("RELAY_LOG_LEVEL must be debug, info, warn or error")
	}
	return c, nil
}

// This is a restricted data file, never shell input. Do not include raw values
// or parser errors in diagnostics, because they could contain credentials.
func readDeploymentFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read RELAY_DEPLOYMENT_FILE; restore the deployment secrets volume")
	}
	defer f.Close()
	allowed := map[string]bool{"RELAY_DATABASE_URL": true, "RELAY_ENCRYPTION_KEY": true, "RELAY_INITIAL_PASSWORD": true}
	values := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		name, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok || !allowed[name] || value == "" || values[name] != "" {
			return nil, fmt.Errorf("invalid RELAY_DEPLOYMENT_FILE; refusing to replace stored secrets")
		}
		values[name] = value
	}
	if scanner.Err() != nil {
		return nil, fmt.Errorf("cannot read RELAY_DEPLOYMENT_FILE; restore the deployment secrets volume")
	}
	return values, nil
}

// RemoveInitialPassword removes the disposable credential after its database
// transaction commits. This is idempotent so startup can retry interrupted
// cleanup. Errors are deliberately redacted; callers must not undo a successful
// password change because file cleanup failed.
func RemoveInitialPassword(deploymentFile, passwordFile string) error {
	if passwordFile != "" {
		if info, err := os.Lstat(passwordFile); err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("refusing to remove a non-regular initial password file")
			}
			if err := os.Remove(passwordFile); err != nil {
				return fmt.Errorf("cannot remove initial password file; check deployment file permissions")
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("cannot inspect initial password file")
		}
	}
	if deploymentFile == "" {
		return nil
	}
	info, err := os.Lstat(deploymentFile)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("cannot inspect deployment configuration for initial password cleanup")
	}
	values, err := readDeploymentFile(deploymentFile)
	if err != nil {
		return err
	}
	if _, exists := values["RELAY_INITIAL_PASSWORD"]; !exists {
		return nil
	}
	file, err := os.CreateTemp(filepath.Dir(deploymentFile), ".deployment-cleanup-*")
	if err != nil {
		return fmt.Errorf("cannot clean initial password from deployment configuration; check directory permissions")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	for _, name := range []string{"RELAY_DATABASE_URL", "RELAY_ENCRYPTION_KEY"} {
		if value, ok := values[name]; ok {
			if _, err := fmt.Fprintf(file, "%s=%s\n", name, value); err != nil {
				return fmt.Errorf("cannot write cleaned deployment configuration")
			}
		}
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("cannot persist cleaned deployment configuration")
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("cannot close cleaned deployment configuration")
	}
	if err := os.Rename(file.Name(), deploymentFile); err != nil {
		return fmt.Errorf("cannot replace deployment configuration during initial password cleanup")
	}
	return nil
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
