package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicURLIsOptionalForDirectNASAccess(t *testing.T) {
	settings := map[string]string{"RELAY_DATABASE_URL": "postgres://app:password@postgres/relay", "RELAY_ENCRYPTION_KEY": strings.Repeat("ab", 32)}
	c, err := LoadWith(func(k string) string { return settings[k] })
	if err != nil || c.PublicURL != "" || c.SecureCookies {
		t.Fatalf("direct NAS config=%+v err=%v", c, err)
	}
	settings["RELAY_SECURE_COOKIES"] = "true"
	c, err = LoadWith(func(k string) string { return settings[k] })
	if err != nil || !c.SecureCookies {
		t.Fatalf("HTTPS proxy without fixed host: %v", err)
	}
}

func TestDeploymentFileRestoresGeneratedSecretsWithoutShellEvaluation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deployment.env")
	contents := "RELAY_DATABASE_URL=postgres://app:original-secret@postgres:5432/relay?sslmode=disable\nRELAY_ENCRYPTION_KEY=" + strings.Repeat("ab", 32) + "\nRELAY_INITIAL_PASSWORD=" + strings.Repeat("a", 64) + "\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{"RELAY_DEPLOYMENT_FILE": path}
	get := func(k string) string { return settings[k] }
	c, err := LoadWith(get)
	if err != nil || len(c.EncryptionKey) != 32 || len(c.InitialPassword) != 64 {
		t.Fatalf("generated config error=%v", err)
	}
	settings["RELAY_ENCRYPTION_KEY"] = strings.Repeat("cd", 32)
	c, err = LoadWith(get)
	if err != nil || c.EncryptionKey[0] != 0xcd {
		t.Fatalf("explicit override error=%v", err)
	}
	for _, invalid := range []string{contents + "UNKNOWN=original-secret\n", strings.Replace(contents, "RELAY_INITIAL_PASSWORD=", "RELAY_ENCRYPTION_KEY=", 1), "RELAY_DATABASE_URL=$(original-secret)\n"} {
		if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadWith(get); err == nil || strings.Contains(err.Error(), "original-secret") {
			t.Fatalf("invalid deployment file error=%v", err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWith(get); err == nil {
		t.Fatal("missing secrets were accepted")
	}
}

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

func TestDatabaseConnectionSelection(t *testing.T) {
	for _, test := range []struct {
		name, databaseURL string
		invalid           bool
	}{
		{name: "SQLite"},
		{name: "single PostgreSQL account", databaseURL: "postgres://relay_app:test-only@postgres/relay"},
		{name: "missing database", databaseURL: "postgres://relay_app:test-only@postgres", invalid: true},
		{name: "missing host", databaseURL: "postgres:///relay", invalid: true},
		{name: "wrong protocol", databaseURL: "mysql://relay_app:test-only@postgres/relay", invalid: true},
		{name: "management database", databaseURL: "postgres://relay_app:test-only@postgres/postgres", invalid: true},
		{name: "template database", databaseURL: "postgres://relay_app:test-only@postgres/template0", invalid: true},
		{name: "nested database path", databaseURL: "postgres://relay_app:test-only@postgres/relay/other", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := LoadWith(func(name string) string {
				if name == "RELAY_DATABASE_URL" {
					return test.databaseURL
				}
				return ""
			})
			if (err != nil) != test.invalid {
				t.Fatalf("configuration error=%v, invalid=%v", err, test.invalid)
			}
			if err == nil && cfg.DatabaseURL != test.databaseURL {
				t.Fatal("database connection was not preserved")
			}
			if err != nil && strings.Contains(err.Error(), "test-only") {
				t.Fatal("configuration error disclosed database credentials")
			}
		})
	}
}

func TestPartialDeploymentSettingsAndConsumedInitialFile(t *testing.T) {
	folder := t.TempDir()
	path := filepath.Join(folder, "deployment.env")
	passwordFile := filepath.Join(folder, "password")
	database := "postgres://app:original-secret@postgres/relay"
	key := strings.Repeat("ab", 32)
	settings := map[string]string{"RELAY_DEPLOYMENT_FILE": path, "RELAY_DATABASE_URL": database, "RELAY_ENCRYPTION_KEY": key, "RELAY_INITIAL_PASSWORD_FILE": passwordFile}
	get := func(k string) string { return settings[k] }
	for _, contents := range []string{"RELAY_DATABASE_URL=" + database + "\n", "RELAY_ENCRYPTION_KEY=" + key + "\n", ""} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		c, err := LoadWith(get)
		if err != nil || c.InitialPassword != "" {
			t.Fatalf("partial config: %v", err)
		}
	}
	durable := "RELAY_DATABASE_URL=" + database + "\nRELAY_ENCRYPTION_KEY=" + key + "\n"
	if err := os.WriteFile(path, []byte(durable), 0600); err != nil {
		t.Fatal(err)
	}
	initial := strings.Repeat("a", 64)
	if err := os.WriteFile(passwordFile, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	if c, err := LoadWith(get); err != nil || c.InitialPassword != initial {
		t.Fatalf("initial file load: %v", err)
	}
	if err := RemoveInitialPassword(path, passwordFile); err != nil {
		t.Fatal(err)
	}
	if err := RemoveInitialPassword(path, passwordFile); err != nil {
		t.Fatal("cleanup not idempotent:", err)
	}
	if c, err := LoadWith(get); err != nil || c.InitialPassword != "" {
		t.Fatalf("consumed file reload: %v", err)
	}
	if contents, _ := os.ReadFile(path); string(contents) != durable {
		t.Fatal("cleanup changed durable config")
	}
	if err := os.WriteFile(path, []byte(durable+"RELAY_INITIAL_PASSWORD="+initial+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveInitialPassword(path, ""); err != nil {
		t.Fatal(err)
	}
	if contents, _ := os.ReadFile(path); string(contents) != durable {
		t.Fatal("legacy credential remains/durable config changed")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("cleaned config not private")
	}
	delete(settings, "RELAY_ENCRYPTION_KEY")
	if err := os.WriteFile(path, []byte("RELAY_DATABASE_URL="+database+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWith(get); err == nil {
		t.Fatal("accepted missing effective encryption key")
	}
}

func TestInitialCleanupRejectsLinksAndPreservesInvalidConfig(t *testing.T) {
	folder := t.TempDir()
	original := filepath.Join(folder, "original")
	link := filepath.Join(folder, "password")
	if err := os.WriteFile(original, []byte("preserve-this-content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(original, link); err != nil {
		t.Fatal(err)
	}
	if err := RemoveInitialPassword("", link); err == nil {
		t.Fatal("accepted symlink")
	}
	if err := RemoveInitialPassword(original, ""); err == nil || strings.Contains(err.Error(), "preserve-this-content") {
		t.Fatal("invalid config accepted or disclosed")
	}
	if contents, _ := os.ReadFile(original); string(contents) != "preserve-this-content" {
		t.Fatal("cleanup changed original")
	}
}
