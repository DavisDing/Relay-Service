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
	contents := "RELAY_DATABASE_URL=postgres://app:original-secret@postgres:5432/relay?sslmode=disable\nRELAY_MIGRATION_DATABASE_URL=postgres://migrator:original-secret@postgres:5432/relay?sslmode=disable\nRELAY_ENCRYPTION_KEY=" + strings.Repeat("ab", 32) + "\nRELAY_INITIAL_PASSWORD=" + strings.Repeat("a", 64) + "\n"
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

func TestStartupMigrationConnectionMustUseSameDatabaseAndOptions(t *testing.T) {
	application := "postgres://app:business-secret@postgres:5432/relay?sslmode=disable&search_path=public"
	for _, test := range []struct {
		name, migration string
		invalid         bool
	}{
		{name: "disabled"},
		{name: "different credentials", migration: "postgresql://migrator:setup-secret@POSTGRES/relay?search_path=public&sslmode=disable"},
		{name: "wrong host", migration: "postgres://migrator:setup-secret@other/relay?sslmode=disable&search_path=public", invalid: true},
		{name: "wrong port", migration: "postgres://migrator:setup-secret@postgres:5433/relay?sslmode=disable&search_path=public", invalid: true},
		{name: "wrong database", migration: "postgres://migrator:setup-secret@postgres/other?sslmode=disable&search_path=public", invalid: true},
		{name: "wrong schema", migration: "postgres://migrator:setup-secret@postgres/relay?sslmode=disable&search_path=other", invalid: true},
		{name: "host override", migration: "postgres://migrator:setup-secret@postgres/relay?sslmode=disable&search_path=public&host=other", invalid: true},
		{name: "shared database", migration: "postgres://migrator:setup-secret@postgres/postgres", invalid: true},
		{name: "invalid URL", migration: "invalid-setup-secret", invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := LoadWith(func(key string) string {
				switch key {
				case "RELAY_DATABASE_URL":
					return application
				case "RELAY_MIGRATION_DATABASE_URL":
					return test.migration
				case "RELAY_ENCRYPTION_KEY":
					return strings.Repeat("ab", 32)
				}
				return ""
			})
			if (err != nil) != test.invalid {
				t.Fatalf("LoadWith error=%v; invalid=%v", err, test.invalid)
			}
			if err == nil && cfg.MigrationDatabaseURL != test.migration {
				t.Fatal("startup migration connection was not preserved")
			}
			if err != nil && (strings.Contains(err.Error(), "setup-secret") || strings.Contains(err.Error(), "business-secret")) {
				t.Fatal("configuration error contains a database credential")
			}
		})
	}
}
