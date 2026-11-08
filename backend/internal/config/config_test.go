package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadMapsSharedCodexClientPaths(t *testing.T) {
	values := map[string]string{
		"BACKEND_DATABASE_URL":              "postgres://product",
		"BACKEND_CODEX_PATH":                "/opt/codex/bin/codex",
		"BACKEND_CODEX_CODE_MODE_HOST_PATH": "/opt/codex/bin/code-mode",
		"BACKEND_CODEX_CWD":                 "/srv/canonkeeper/agent-workspace",
		"BACKEND_CODEX_REPOSITORY_HOME":     "/srv/canonkeeper/codex-home",
		"BACKEND_CODEX_HOME":                "/var/lib/canonkeeper/codex-home",
		"BACKEND_CODEX_SQLITE_HOME":         "/var/lib/canonkeeper/codex-sqlite",
		"BACKEND_CODEX_PRIVATE_HOME":        "/var/lib/canonkeeper/private-home",
		"BACKEND_CODEX_TEMP_DIR":            "/var/lib/canonkeeper/tmp",
		"BACKEND_MODEL_API_BASE_URL":        "https://models.example.test/backend-api/codex",
		"BACKEND_MODEL_API_KEY_ENV":         "CODEX_LB_API_KEY",
		"BACKEND_MODEL_API_MODEL":           "gpt-6-luna",
		"BACKEND_MODEL_API_CHECK_MODEL":     "gpt-6.1-sol",
		"BACKEND_MODEL_API_PARALLEL":        "16",
		// specs/2026-11-06-writing-agent.md WA5.11: the writing agent writes with one model and checks with another.
		"BACKEND_MODEL_API_WRITING_MODEL":       "gpt-6.1-sol",
		"BACKEND_MODEL_API_WRITING_CHECK_MODEL": "gpt-6.1-terra",
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
	config := Load()
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if config.CodexPath != values["BACKEND_CODEX_PATH"] || config.CodexHome != values["BACKEND_CODEX_HOME"] ||
		config.CodexRepositoryHome != values["BACKEND_CODEX_REPOSITORY_HOME"] {
		t.Fatalf("Codex paths were not mapped: %#v", config)
	}
	if config.ModelAPI != (ModelAPI{
		BaseURL: values["BACKEND_MODEL_API_BASE_URL"], KeyEnv: "CODEX_LB_API_KEY", Model: "gpt-6-luna", CheckModel: "gpt-6.1-sol", Parallel: 16,
		WritingModel: "gpt-6.1-sol", WritingCheckModel: "gpt-6.1-terra",
	}) {
		t.Fatalf("model API was not mapped: %#v", config.ModelAPI)
	}
}

func TestValidateRequiresTheModelAPI(t *testing.T) {
	config := validConfig()
	config.ModelAPI = ModelAPI{}
	if err := config.Validate(); err == nil {
		t.Fatal("a configuration without the model API for cards and scans was accepted")
	}
}

// specs/2026-11-07-desktop-app.md DA2.6, DA16.4: the open core runs without mail, without the writing agent's models
// and without a model API connected yet; the site still requires all three.
func TestTheCoreRunsWithoutTheSitesMailWritingModelsAndConnectedModel(t *testing.T) {
	config := validConfig()
	config.Mail = Mail{}
	config.ModelAPI = ModelAPI{Model: "canonkeeper-chat", CardsModel: "canonkeeper-cards"}
	if err := config.ValidateCore(); err != nil {
		t.Fatalf("ValidateCore() = %v", err)
	}
	if err := config.Validate(); err == nil {
		t.Fatal("the site was accepted without mail and a model API")
	}
	config.ModelAPI.BaseURL = "/relative"
	if err := config.ValidateCore(); err == nil {
		t.Fatal("the core accepted a relative model API URL")
	}
}

func TestValidateRejectsRelativeOrSharedCodexHomes(t *testing.T) {
	config := validConfig()
	config.CodexCWD = "relative"
	if err := config.Validate(); err == nil {
		t.Fatal("relative Codex path was accepted")
	}
	config = validConfig()
	config.CodexHome = config.CodexRepositoryHome
	if err := config.Validate(); err == nil {
		t.Fatal("repository home was accepted as writable runtime home")
	}
}

func TestValidateRequiresSeparateWorkerDatabase(t *testing.T) {
	config := validConfig()
	config.WorkersEnabled = true
	config.WorkerDatabaseURL = config.DatabaseURL
	if err := config.Validate(); err == nil {
		t.Fatal("shared product/worker database identity was accepted")
	}
}

func TestLoadFileMapsCodexPathsAndRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	payload := `{
		"http_addr":"127.0.0.1:8788","environment":"test","database_url":"postgres://product",
		"worker_database_url":"","workers_enabled":false,
		"allowed_origins":["http://localhost:3000"],"secure_cookies":false,
		"file_storage_dir":"data/files","platform_admin_user_id":"","promo_operator_user_ids":["operator-account"],
		"codex":{"path":"/opt/codex","code_mode_host_path":"","cwd":"/srv/work",
		"repository_home":"/srv/repository-home","home":"/srv/runtime-home",
		"sqlite_home":"/srv/sqlite","private_home":"/srv/private","temp_dir":"/srv/tmp"},
		"model_api":{"base_url":"https://models.example.test/backend-api/codex","model":"gpt-6-luna",
		"writing_model":"gpt-6.1-sol","writing_check_model":"gpt-6.1-terra"},
		"mail":{"mode":"outbox","public_url":"http://test.localhost:3334","from":"support@canonkeeper.ru","outbox_dir":"/srv/mail-outbox"}}
	`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(); err != nil || config.CodexSQLiteHome != "/srv/sqlite" || !slices.Equal(config.PromoOperatorUserIDs, []string{"operator-account"}) {
		t.Fatalf("LoadFile() = %#v, %v", config, err)
	}
	unknown := strings.Replace(payload, `"temp_dir":"/srv/tmp"`, `"temp_dir":"/srv/tmp","legacy_runtime":true`, 1)
	if err := os.WriteFile(path, []byte(unknown), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil {
		t.Fatal("unknown legacy runtime field was accepted")
	}
	// specs/2026-11-06-writing-agent.md WA13: the working judge of the earlier writing is gone, and so is its key.
	judge := strings.Replace(payload, `"model":"gpt-6-luna",`, `"model":"gpt-6-luna","judge_model":"gpt-6.1-sol",`, 1)
	if err := os.WriteFile(path, []byte(judge), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil {
		t.Fatal("the removed judge_model was accepted")
	}
}

func validConfig() Config {
	return Config{
		Environment: "test", DatabaseURL: "postgres://product",
		CodexPath: "/opt/codex", CodexCWD: "/srv/work", CodexRepositoryHome: "/srv/repository-home",
		CodexHome: "/srv/runtime-home", CodexSQLiteHome: "/srv/sqlite",
		CodexPrivateHome: "/srv/private", CodexTempDir: "/srv/tmp",
		ModelAPI: ModelAPI{BaseURL: "https://models.example.test/backend-api/codex", Model: "gpt-6-luna", WritingModel: "gpt-6.1-sol", WritingCheckModel: "gpt-6.1-terra"},
		Mail:     Mail{Mode: MailModeOutbox, PublicURL: "http://test.localhost:3334", From: "support@canonkeeper.ru", OutboxDir: "/srv/mail-outbox"},
	}
}

func TestLoadFileMapsModelEnvironmentSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	payload := `{
		"http_addr":"127.0.0.1:8789","environment":"test","database_url":"postgres://product",
		"allowed_origins":["http://test.localhost:3334"],"file_storage_dir":"data/files",
		"codex":{"path":"/opt/codex","cwd":"/srv/work","repository_home":"/srv/repository-home",
		"home":"/srv/runtime-home","sqlite_home":"/srv/sqlite","private_home":"/srv/private","temp_dir":"/srv/tmp",
		"config_overrides":["model=\"gpt-6-luna\"","model_provider=\"codex-lb\""],"pass_env":["CODEX_LB_API_KEY"]},
		"model_api":{"base_url":"https://models.example.test/backend-api/codex","key_env":"CODEX_LB_API_KEY",
		"model":"gpt-6-luna","effort":"medium","cards_model":"gpt-6.1-sol","cards_effort":"high",
		"image_model":"gpt-image-2","image_quality":"medium","check_model":"gpt-6.1-sol","parallel":16,
		"writing_model":"gpt-6.1-sol","writing_check_model":"gpt-6.1-terra",
		"voice_url":"https://models.example.test","voice_model":"gpt-live-1-codex"},
		"mail":{"mode":"outbox","public_url":"http://test.localhost:3334","from":"support@canonkeeper.ru","outbox_dir":"/srv/mail-outbox"}}
	`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(config.CodexConfigOverrides) != 2 || config.CodexPassEnv[0] != "CODEX_LB_API_KEY" ||
		!config.ModelAPI.Enabled() || config.ModelAPI.Model != "gpt-6-luna" || config.ModelAPI.Parallel != 16 ||
		config.ModelAPI.CardsModel != "gpt-6.1-sol" || config.ModelAPI.CardsEffort != "high" ||
		config.ModelAPI.ImageModel != "gpt-image-2" || config.ModelAPI.ImageQuality != "medium" ||
		config.ModelAPI.CheckModel != "gpt-6.1-sol" || config.ModelAPI.VoiceURL != "https://models.example.test" {
		t.Fatalf("LoadFile() = %#v", config)
	}
	// specs/2026-11-03-voice-assistant.md V4.1: voice takes the same key; the model is the configured one.
	if voiceModel := config.ModelAPI.Voice(); voiceModel != "gpt-live-1-codex" {
		t.Fatalf("Voice() = %s", voiceModel)
	}
	config.ModelAPI.ImageQuality = "ultra"
	if err := config.Validate(); err == nil {
		t.Fatal("an unknown image quality was accepted")
	}
}

func TestValidateRejectsUnsafeModelEnvironmentSettings(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"override without value":   func(c *Config) { c.CodexConfigOverrides = []string{"model="} },
		"multi-line override":      func(c *Config) { c.CodexConfigOverrides = []string{"model=\"x\"\nshell=true"} },
		"replaces fixed home":      func(c *Config) { c.CodexPassEnv = []string{"HOME"} },
		"lower-case variable name": func(c *Config) { c.CodexPassEnv = []string{"codex_key"} },
		"relative model API URL":   func(c *Config) { c.ModelAPI = ModelAPI{BaseURL: "/backend-api/codex", Model: "m"} },
		"model API without model":  func(c *Config) { c.ModelAPI = ModelAPI{BaseURL: "https://models.example.test"} },
		"wss voice URL":            func(c *Config) { c.ModelAPI.VoiceURL = "wss://models.example.test" },
		"voice URL with a path":    func(c *Config) { c.ModelAPI.VoiceURL = "https://models.example.test/v1/realtime" },
		// specs/2026-11-06-writing-agent.md WA5.11, WA13: the writing agent writes with one model and checks with another.
		"no writing model":       func(c *Config) { c.ModelAPI.WritingModel = "" },
		"no writing check model": func(c *Config) { c.ModelAPI.WritingCheckModel = "" },
		"one writing model":      func(c *Config) { c.ModelAPI.WritingCheckModel = c.ModelAPI.WritingModel },
	} {
		config := validConfig()
		mutate(&config)
		if err := config.Validate(); err == nil {
			t.Errorf("%s: Validate() accepted unsafe settings", name)
		}
	}
}

// specs/2026-10-29-saga-illustrations.md R15.5: uploads are switched on only by the file section
// or the environment; an absent section keeps them off.
func TestIllustrationUploadsAreOffUnlessSwitchedOn(t *testing.T) {
	base := `"http_addr":"127.0.0.1:8789","environment":"test","database_url":"postgres://product",
		"allowed_origins":["http://test.localhost:3334"],"file_storage_dir":"data/files",
		"mail":{"mode":"outbox","public_url":"http://test.localhost:3334","from":"support@canonkeeper.ru","outbox_dir":"/srv/mail-outbox"}`
	for payload, want := range map[string]bool{
		`{` + base + `}`: false,
		`{` + base + `,"illustrations":{"uploads_enabled":true}}`:  true,
		`{` + base + `,"illustrations":{"uploads_enabled":false}}`: false,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		config, err := LoadFile(path)
		if err != nil || config.Illustrations.UploadsEnabled != want {
			t.Fatalf("LoadFile(%s) uploads = %v, %v; want %v", payload, config.Illustrations.UploadsEnabled, err, want)
		}
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{`+base+`,"illustrations":{"uploads":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil {
		t.Fatal("an unknown switch of the illustrations was accepted")
	}
	t.Setenv("BACKEND_ILLUSTRATIONS_UPLOADS_ENABLED", "")
	if Load().Illustrations.UploadsEnabled {
		t.Fatal("uploads are on without the environment")
	}
	t.Setenv("BACKEND_ILLUSTRATIONS_UPLOADS_ENABLED", "true")
	if !Load().Illustrations.UploadsEnabled {
		t.Fatal("the environment did not switch uploads on")
	}
}
