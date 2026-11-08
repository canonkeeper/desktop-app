package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func productionFileForTest(t *testing.T, mailSection string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backend.json")
	payload := `{
		"http_addr":"127.0.0.1:8788","environment":"production","database_url":"postgres://product",
		"allowed_origins":["https://canonkeeper.ru"],"secure_cookies":true,"file_storage_dir":"/srv/files",
		"codex":{"path":"/opt/codex","cwd":"/srv/work","repository_home":"/srv/repository-home",
		"home":"/srv/runtime-home","sqlite_home":"/srv/sqlite","private_home":"/srv/private","temp_dir":"/srv/tmp"},
		"model_api":{"base_url":"https://models.example.test/backend-api/codex","model":"gpt-6-luna","writing_model":"gpt-6.1-sol","writing_check_model":"gpt-6.1-terra"}` + mailSection + `}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func passwordFileForTest(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "smtp-password")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func smtpSection(passwordFile string, mutate func(map[string]string)) string {
	values := map[string]string{
		"mode": "smtp", "public_url": "https://canonkeeper.ru", "from": "support@canonkeeper.ru",
		"username": "support@canonkeeper.ru", "port": "465", "password_file": passwordFile, "outbox_dir": "",
	}
	if mutate != nil {
		mutate(values)
	}
	return fmt.Sprintf(`,"mail":{"mode":%q,"public_url":%q,"from":%q,"outbox_dir":%q,
		"smtp":{"host":"smtp.timeweb.ru","port":%s,"username":%q,"password_file":%q}}`,
		values["mode"], values["public_url"], values["from"], values["outbox_dir"], values["port"], values["username"], values["password_file"])
}

// R4.4.1, R4.4.2: production mail is SMTP from backend.json with a private password file.
func TestLoadFileReadsProductionSMTPMail(t *testing.T) {
	passwordFile := passwordFileForTest(t, "p@ss w0rd;$x\n", 0o600)
	config, err := LoadFile(productionFileForTest(t, smtpSection(passwordFile, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	mail := config.Mail
	if mail.Mode != MailModeSMTP || mail.From != "support@canonkeeper.ru" || mail.SMTP.Username != "support@canonkeeper.ru" ||
		mail.SMTP.Host != "smtp.timeweb.ru" || mail.SMTP.Port != 465 || string(mail.SMTP.Password) != "p@ss w0rd;$x" {
		t.Fatalf("mail = %+v (password trimmed: %t)", mail, string(mail.SMTP.Password) == "p@ss w0rd;$x")
	}
	if printed := fmt.Sprintf("%v %+v %#v", config, config, config.Mail.SMTP); strings.Contains(printed, "w0rd") {
		t.Fatal("the SMTP password prints")
	}
}

func TestLoadFileRejectsUnsafeProductionMail(t *testing.T) {
	passwordFile := passwordFileForTest(t, "secret", 0o600)
	for name, section := range map[string]string{
		"no mail section":   "",
		"outbox":            smtpSection(passwordFile, func(v map[string]string) { v["mode"] = "outbox"; v["outbox_dir"] = "/srv/mail" }),
		"http public URL":   smtpSection(passwordFile, func(v map[string]string) { v["public_url"] = "http://canonkeeper.ru" }),
		"public URL path":   smtpSection(passwordFile, func(v map[string]string) { v["public_url"] = "https://canonkeeper.ru/app" }),
		"login is not from": smtpSection(passwordFile, func(v map[string]string) { v["username"] = "other@example.test" }),
		"port 587":          smtpSection(passwordFile, func(v map[string]string) { v["port"] = "587" }),
		"display name from": smtpSection(passwordFile, func(v map[string]string) { v["from"] = "CanonKeeper <support@canonkeeper.ru>" }),
		"relative password": smtpSection(passwordFile, func(v map[string]string) { v["password_file"] = "smtp-password" }),
		"world readable":    smtpSection(passwordFileForTest(t, "secret", 0o644), nil),
		"empty password":    smtpSection(passwordFileForTest(t, "\n", 0o600), nil),
		"missing password":  smtpSection(filepath.Join(t.TempDir(), "absent"), nil),
	} {
		if _, err := LoadFile(productionFileForTest(t, section)); err == nil {
			t.Errorf("%s: LoadFile() accepted unsafe mail settings", name)
		} else if strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: error leaks the password: %v", name, err)
		}
	}
}

// R4.4.4: development writes letters to data/mail-outbox.
func TestLoadUsesDevelopmentOutbox(t *testing.T) {
	t.Setenv("BACKEND_PUBLIC_URL", "")
	mail := Load().Mail
	if mail.Mode != MailModeOutbox || mail.OutboxDir != "data/mail-outbox" || mail.PublicURL != "http://localhost:5173" {
		t.Fatalf("development mail = %+v", mail)
	}
	if err := mail.validate("development"); err != nil {
		t.Fatal(err)
	}
}
