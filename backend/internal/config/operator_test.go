package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// productionLikePayload names an SMTP key file that does not exist: the backend refuses to
// start without it, the operator command does not read it.
const productionLikePayload = `{
	"http_addr":"127.0.0.1:8788","environment":"production","database_url":"postgres://product",
	"worker_database_url":"postgres://worker","workers_enabled":true,
	"allowed_origins":["https://canonkeeper.ru"],"secure_cookies":true,
	"file_storage_dir":"/srv/state/exports","platform_admin_user_id":"237a978f-5543-4d6d-a6f0-da4a27b593d2",
	"codex":{"path":"/opt/codex","cwd":"/srv/work","repository_home":"/srv/repository-home",
	"home":"/srv/runtime-home","sqlite_home":"/srv/sqlite","private_home":"/srv/private","temp_dir":"/srv/tmp"},
	"model_api":{"base_url":"https://models.example.test/backend-api/codex","model":"gpt-6-luna"},
	"mail":{"mode":"smtp","public_url":"https://canonkeeper.ru","from":"support@canonkeeper.ru",
	"smtp":{"host":"smtp.example.test","port":465,"username":"support@canonkeeper.ru","password_file":"/nonexistent/smtp-password"}},
	"operator_socket":"/srv/state/operator.sock"}`

func TestLoadOperatorFileReadsOperatorFieldsWithoutMailSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backend.json")
	if err := os.WriteFile(path, []byte(productionLikePayload), 0o600); err != nil {
		t.Fatal(err)
	}
	operator, err := LoadOperatorFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if operator != (Operator{
		Environment: "production", DatabaseURL: "postgres://product",
		PlatformAdminUserID: "237a978f-5543-4d6d-a6f0-da4a27b593d2",
		OperatorSocket:      "/srv/state/operator.sock", StateDir: filepath.Dir(path),
		ModelAPI: ModelAPI{BaseURL: "https://models.example.test/backend-api/codex", Model: "gpt-6-luna"},
	}) {
		t.Fatalf("LoadOperatorFile() = %#v", operator)
	}
	if _, err := LoadFile(path); err == nil {
		t.Fatal("the backend loader no longer reads the SMTP key file; the fixture lost its point")
	}
}

func TestLoadOperatorFileRejectsUnknownFieldsAndRelativeSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backend.json")
	for name, payload := range map[string]string{
		"unknown field":   strings.Replace(productionLikePayload, `"operator_socket"`, `"legacy":1,"operator_socket"`, 1),
		"relative socket": strings.Replace(productionLikePayload, `"/srv/state/operator.sock"`, `"operator.sock"`, 1),
		"no database":     strings.Replace(productionLikePayload, `"postgres://product"`, `""`, 1),
	} {
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOperatorFile(path); err == nil {
			t.Errorf("%s: LoadOperatorFile accepted the file", name)
		}
	}
}

func TestLoadFileMapsOperatorSocket(t *testing.T) {
	config := validConfig()
	config.OperatorSocket = "operator.sock"
	if err := config.Validate(); err == nil {
		t.Fatal("relative operator socket was accepted")
	}
	config.OperatorSocket = "/srv/state/operator.sock"
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
}
