package config

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	agentdomain "canonkeeper/backend/internal/domain/agent"
)

type Config struct {
	HTTPAddr             string
	Environment          string
	DatabaseURL          string
	WorkerDatabaseURL    string
	WorkersEnabled       bool
	AllowedOrigins       []string
	SecureCookies        bool
	FileStorageDir       string
	PlatformAdminUserID  string
	PromoOperatorUserIDs []string
	CodexPath            string
	CodexCodeModeHost    string
	CodexCWD             string
	CodexRepositoryHome  string
	CodexHome            string
	// CodexHomeCopies copies the profile into the homes of Codex instead of linking it (specs/2026-11-13-desk-home.md
	// HM1.1): only the app sets it — its bundle lies at another place from run to run, and a link needs a privilege on
	// Windows; the site links.
	CodexHomeCopies      bool
	CodexSQLiteHome      string
	CodexPrivateHome     string
	CodexTempDir         string
	CodexConfigOverrides []string
	CodexPassEnv         []string
	// CodexEnvironment are NAME=value pairs for the app-servers alone, by value (specs/2026-11-13-desk-gateway.md GW2.2):
	// only the app sets them — the token of its local gateway — and never from the environment of the backend.
	CodexEnvironment []string
	ModelAPI         ModelAPI
	Mail             Mail
	// OperatorSocket is the unix socket the operator command reaches the backend through;
	// empty disables it (specs/2026-10-28-account-data-rights.md, R2.1).
	OperatorSocket string
	// Illustrations switches the parts of the saga gallery a rollback may turn off.
	Illustrations Illustrations
	// AppMirror is the mirror of the releases of the app on the site; off without its section.
	AppMirror AppMirror
}

// AppMirror is the mirror of the releases of the app the site keeps (specs/2026-11-16-desk-app-mirror.md AM3, AM4): the
// directory of its files and the repository on GitHub it follows. Without a directory it is off: the desktop build makes
// its configuration itself and never sets it.
type AppMirror struct {
	Dir        string
	Repository string
}

// Enabled reports whether the site keeps the mirror.
func (mirror AppMirror) Enabled() bool { return mirror.Dir != "" }

func (mirror AppMirror) validate() error {
	if (mirror.Dir != "" || mirror.Repository != "") && (!filepath.IsAbs(mirror.Dir) || strings.TrimSpace(mirror.Repository) == "") {
		return errors.New("config: the mirror of the app needs an absolute directory and a repository")
	}
	return nil
}

// Illustrations are the switches of the saga gallery (specs/2026-10-29-saga-illustrations.md).
type Illustrations struct {
	// UploadsEnabled lets authors upload their own images (UC15, R15.5); off by default.
	UploadsEnabled bool
}

// ModelAPI configures direct model calls made by the backend itself (chapter cards,
// exhaustive scans, drawings of the saga gallery). The key is read from the named environment variable at use time.
type ModelAPI struct {
	BaseURL string
	KeyEnv  string
	// Key and CarryJob are the client of the app (specs/2026-11-13-desk-gateway.md GW3.1, GW3.2): the token of its local
	// gateway by value, never an environment, and the Job of a call named to the gateway. The site sets neither.
	Key      string
	CarryJob bool
	// Stops and RetryPause are the client of the app too (specs/2026-11-13-desk-consent.md CN3.4): a stop of the reading
	// the gateway names is typed and not repeated; RetryPause shortens the pauses of the repeats in the program of the
	// process tests alone.
	Stops      bool
	RetryPause time.Duration
	Model      string
	Effort     string
	// CardsModel and CardsEffort write chapter cards; empty, cards use Model and Effort.
	CardsModel  string
	CardsEffort string
	// ImageModel and ImageQuality draw the saga gallery
	// (specs/2026-10-29-saga-illustrations.md R1.2); empty, Model and medium.
	ImageModel   string
	ImageQuality string
	// CheckModel looks at a drawing and checks it against the facts of the text (R12.2, R13.11);
	// empty, Model.
	CheckModel string
	// WritingModel, WritingEffort and WritingCheckModel are the writing agent (specs/2026-11-06-writing-agent.md, WA5.11):
	// the model that writes the scenes, at WritingEffort or high, and the model that checks them. Both are required and
	// are two different models.
	WritingModel      string
	WritingEffort     string
	WritingCheckModel string
	Parallel          int
	// VoiceURL is the gateway of GPT-Live, https://host[:port], with the same key (specs/2026-11-03-voice-assistant.md
	// V4.1); empty, voice is off. VoiceModel is the voice; empty, gpt-live-1.1-codex. VoiceBrain and VoiceBrainEffort
	// are the assistant the voice hands its work to and the writer of the summaries (V4.3, V4.5); empty, gpt-6.1-sol
	// and low.
	VoiceURL         string
	VoiceModel       string
	VoiceBrain       string
	VoiceBrainEffort string
	// AppKeyEnv names the variable of the key of the provider for the work of linked apps on the models of the site
	// (specs/2026-11-13-desk-proxy.md; specs/2026-11-12-desktop-target.md DT4.9): its own key with its own limit at the
	// provider, never the key of the site; the value is read only when an app calls. Empty, the site serves no models to
	// apps. AppParallel is the number of requests of all apps at once (AppPlaces).
	AppKeyEnv   string
	AppParallel int
}

// Enabled reports whether direct model calls are configured.
func (api ModelAPI) Enabled() bool { return strings.TrimSpace(api.BaseURL) != "" }

type settings struct {
	HTTPAddr             string
	Environment          string
	DatabaseURL          string
	WorkerDatabaseURL    string
	WorkersEnabled       bool
	AllowedOrigins       []string
	SecureCookies        bool
	FileStorageDir       string
	PlatformAdminUserID  string
	PromoOperatorUserIDs []string
	CodexPath            string
	CodexCodeModeHost    string
	CodexCWD             string
	CodexRepositoryHome  string
	CodexHome            string
	CodexSQLiteHome      string
	CodexPrivateHome     string
	CodexTempDir         string
	CodexConfigOverrides []string
	CodexPassEnv         []string
	ModelAPI             ModelAPI
	Mail                 Mail
	OperatorSocket       string
	Illustrations        Illustrations
	AppMirror            AppMirror
}

func Load() Config {
	addr := os.Getenv("BACKEND_HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8788"
	}
	env := os.Getenv("BACKEND_ENV")
	if env == "" {
		env = "development"
	}
	allowedOrigins := splitAllowedOrigins(os.Getenv("BACKEND_ALLOWED_ORIGINS"))
	if len(allowedOrigins) == 0 {
		allowedOrigins = []string{
			"http://localhost:3000", "http://127.0.0.1:3000", "http://[::1]:3000",
			"http://localhost:5173", "http://127.0.0.1:5173", "http://[::1]:5173",
		}
	}
	secureCookies := secureCookiesDefault(env)
	if raw, ok := os.LookupEnv("BACKEND_SECURE_COOKIES"); ok {
		secureCookies = parseBool(raw)
	}
	fileStorageDir := strings.TrimSpace(os.Getenv("BACKEND_FILE_STORAGE_DIR"))
	if fileStorageDir == "" {
		fileStorageDir = "data/files"
	}
	return newConfig(settings{
		HTTPAddr: addr, Environment: env,
		DatabaseURL:       strings.TrimSpace(os.Getenv("BACKEND_DATABASE_URL")),
		WorkerDatabaseURL: strings.TrimSpace(os.Getenv("BACKEND_WORKER_DATABASE_URL")),
		WorkersEnabled:    parseBool(os.Getenv("BACKEND_WORKERS_ENABLED")),
		AllowedOrigins:    allowedOrigins, SecureCookies: secureCookies, FileStorageDir: fileStorageDir,
		PlatformAdminUserID:  strings.TrimSpace(os.Getenv("BACKEND_PLATFORM_ADMIN_USER_ID")),
		PromoOperatorUserIDs: splitAllowedOrigins(os.Getenv("BACKEND_PROMO_OPERATOR_USER_IDS")),
		CodexPath:            strings.TrimSpace(os.Getenv("BACKEND_CODEX_PATH")),
		CodexCodeModeHost:    strings.TrimSpace(os.Getenv("BACKEND_CODEX_CODE_MODE_HOST_PATH")),
		CodexCWD:             strings.TrimSpace(os.Getenv("BACKEND_CODEX_CWD")),
		CodexRepositoryHome:  strings.TrimSpace(os.Getenv("BACKEND_CODEX_REPOSITORY_HOME")),
		CodexHome:            strings.TrimSpace(os.Getenv("BACKEND_CODEX_HOME")),
		CodexSQLiteHome:      strings.TrimSpace(os.Getenv("BACKEND_CODEX_SQLITE_HOME")),
		CodexPrivateHome:     strings.TrimSpace(os.Getenv("BACKEND_CODEX_PRIVATE_HOME")),
		CodexTempDir:         strings.TrimSpace(os.Getenv("BACKEND_CODEX_TEMP_DIR")),
		CodexConfigOverrides: splitLines(os.Getenv("BACKEND_CODEX_CONFIG_OVERRIDES")),
		CodexPassEnv:         splitAllowedOrigins(os.Getenv("BACKEND_CODEX_PASS_ENV")),
		ModelAPI: ModelAPI{
			BaseURL:           strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_BASE_URL")),
			KeyEnv:            strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_KEY_ENV")),
			Model:             strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_MODEL")),
			Effort:            strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_EFFORT")),
			CardsModel:        strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_CARDS_MODEL")),
			CardsEffort:       strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_CARDS_EFFORT")),
			ImageModel:        strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_IMAGE_MODEL")),
			ImageQuality:      strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_IMAGE_QUALITY")),
			CheckModel:        strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_CHECK_MODEL")),
			WritingModel:      strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_WRITING_MODEL")),
			WritingEffort:     strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_WRITING_EFFORT")),
			WritingCheckModel: strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_WRITING_CHECK_MODEL")),
			Parallel:          parseParallel(os.Getenv("BACKEND_MODEL_API_PARALLEL")),
			VoiceURL:          strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_VOICE_URL")),
			VoiceModel:        strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_VOICE_MODEL")),
			VoiceBrain:        strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_VOICE_BRAIN")),
			VoiceBrainEffort:  strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_VOICE_BRAIN_EFFORT")),
			AppKeyEnv:         strings.TrimSpace(os.Getenv("BACKEND_MODEL_API_APP_KEY_ENV")),
			AppParallel:       parseParallel(os.Getenv("BACKEND_MODEL_API_APP_PARALLEL")),
		},
		Mail:          developmentMail(),
		Illustrations: Illustrations{UploadsEnabled: parseBool(os.Getenv("BACKEND_ILLUSTRATIONS_UPLOADS_ENABLED"))},
	})
}

// developmentMail writes letters to data/mail-outbox; development never sends mail.
func developmentMail() Mail {
	publicURL := strings.TrimSpace(os.Getenv("BACKEND_PUBLIC_URL"))
	if publicURL == "" {
		publicURL = "http://localhost:5173"
	}
	from := strings.TrimSpace(os.Getenv("BACKEND_MAIL_FROM"))
	if from == "" {
		from = "canonkeeper@localhost"
	}
	return Mail{Mode: MailModeOutbox, PublicURL: publicURL, From: from, OutboxDir: "data/mail-outbox"}
}

// parseParallel reads an optional positive count; an invalid value fails validation.
func parseParallel(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return -1
	}
	return value
}

func splitLines(raw string) []string {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func newConfig(input settings) Config {
	return Config{
		HTTPAddr: input.HTTPAddr, Environment: input.Environment,
		DatabaseURL: input.DatabaseURL, WorkerDatabaseURL: input.WorkerDatabaseURL,
		WorkersEnabled: input.WorkersEnabled,
		AllowedOrigins: append([]string(nil), input.AllowedOrigins...), SecureCookies: input.SecureCookies,
		FileStorageDir: input.FileStorageDir, PlatformAdminUserID: strings.TrimSpace(input.PlatformAdminUserID),
		PromoOperatorUserIDs: append([]string(nil), input.PromoOperatorUserIDs...),
		CodexPath:            input.CodexPath, CodexCodeModeHost: input.CodexCodeModeHost, CodexCWD: input.CodexCWD,
		CodexRepositoryHome: input.CodexRepositoryHome, CodexHome: input.CodexHome,
		CodexSQLiteHome: input.CodexSQLiteHome, CodexPrivateHome: input.CodexPrivateHome, CodexTempDir: input.CodexTempDir,
		CodexConfigOverrides: append([]string(nil), input.CodexConfigOverrides...),
		CodexPassEnv:         append([]string(nil), input.CodexPassEnv...),
		ModelAPI:             input.ModelAPI,
		Mail:                 input.Mail,
		OperatorSocket:       input.OperatorSocket,
		Illustrations:        input.Illustrations,
		AppMirror:            input.AppMirror,
	}
}

// Validate checks the whole configuration of the site: the open core (ValidateCore), its mail and its direct model
// API, which chapter cards, the scan tool and the writing agent depend on.
func (config Config) Validate() error {
	if err := config.ValidateCore(); err != nil {
		return err
	}
	if err := config.Mail.validate(config.Environment); err != nil {
		return err
	}
	if err := config.ModelAPI.validate(); err != nil {
		return err
	}
	if err := config.AppMirror.validate(); err != nil {
		return err
	}
	// The turns of apps run the model and effort of the chat of the site (specs/2026-11-13-desk-proxy.md PX1.3).
	if model, _ := config.Chat(); config.ModelAPI.AppKeyEnv != "" && model == "" {
		return errors.New("config: the models of apps require the chat model of the site in the Codex overrides")
	}
	return nil
}

// ValidateCore checks what the open core runs on in every build (specs/2026-11-07-desktop-app.md DA16.4): the worker
// identity, the Codex paths and settings and the shape of the model API. A build without a base URL of the model API
// has no AI connected yet: the desktop until the author connects one (DA2.6).
func (config Config) ValidateCore() error {
	if config.WorkersEnabled {
		workerURL, productURL := strings.TrimSpace(config.WorkerDatabaseURL), strings.TrimSpace(config.DatabaseURL)
		if workerURL == "" || productURL == "" || workerURL == productURL {
			return errors.New("config: workers require a separate worker database identity")
		}
	}
	for _, path := range []string{
		config.CodexPath, config.CodexCWD, config.CodexRepositoryHome, config.CodexHome,
		config.CodexSQLiteHome, config.CodexPrivateHome, config.CodexTempDir,
	} {
		if !filepath.IsAbs(path) {
			return errors.New("config: Codex paths must be absolute")
		}
	}
	if config.CodexCodeModeHost != "" && !filepath.IsAbs(config.CodexCodeModeHost) {
		return errors.New("config: Codex code-mode host path must be absolute")
	}
	if config.CodexRepositoryHome == config.CodexHome {
		return errors.New("config: repository and writable Codex homes must differ")
	}
	if config.OperatorSocket != "" && !filepath.IsAbs(config.OperatorSocket) {
		return errors.New("config: operator socket path must be absolute")
	}
	if !agentdomain.ValidConfigOverrides(config.CodexConfigOverrides) {
		return errors.New("config: Codex config overrides must be single-line key=value settings")
	}
	if !agentdomain.ValidPassEnv(config.CodexPassEnv) {
		return errors.New("config: Codex pass-through environment names are invalid")
	}
	if !agentdomain.ValidEnvironment(config.CodexEnvironment) {
		return errors.New("config: the Codex environment must be NAME=value pairs of one line")
	}
	return config.ModelAPI.validateShape()
}

// validate requires the direct model API of the site: chapter cards and the scan tool depend on it, and the writing
// agent writes with one model and checks with another.
func (api ModelAPI) validate() error {
	if !api.Enabled() {
		return errors.New("config: model API base URL must be an absolute http(s) URL")
	}
	if err := api.validateShape(); err != nil {
		return err
	}
	if api.WritingModel == "" || api.WritingCheckModel == "" || api.WritingModel == api.WritingCheckModel {
		return errors.New("config: model API requires writing_model and writing_check_model, two different models")
	}
	return nil
}

// validateShape checks the settings of the model API that are set: an absolute http(s) base URL, a model, a
// non-negative parallelism, the quality of drawings, the name of the key variable and the voice URL.
func (api ModelAPI) validateShape() error {
	if api.Enabled() {
		parsed, err := url.Parse(api.BaseURL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			return errors.New("config: model API base URL must be an absolute http(s) URL")
		}
	}
	if strings.TrimSpace(api.Model) == "" || api.Parallel < 0 {
		return errors.New("config: model API requires a model and a non-negative parallelism")
	}
	switch api.ImageQuality {
	case "", "low", "medium", "high", "auto":
	default:
		return errors.New("config: model API image quality must be low, medium, high or auto")
	}
	if api.KeyEnv != "" && !agentdomain.ValidPassEnv([]string{api.KeyEnv}) {
		return errors.New("config: model API key variable name is invalid")
	}
	if api.AppKeyEnv != "" && (!agentdomain.ValidPassEnv([]string{api.AppKeyEnv}) || api.AppKeyEnv == api.KeyEnv) || api.AppParallel < 0 {
		return errors.New("config: the models of apps need a key variable of their own and a non-negative parallelism")
	}
	if api.VoiceURL != "" {
		voice, err := url.Parse(api.VoiceURL)
		if err != nil || (voice.Scheme != "https" && voice.Scheme != "http") || voice.Host == "" || strings.Trim(voice.Path, "/") != "" {
			return errors.New("config: model API voice URL must be an absolute http(s) URL of a host")
		}
	}
	return nil
}

// AppPlaces is how many requests of all linked apps go to the provider at once (specs/2026-11-13-desk-proxy.md PX1.5):
// AppParallel, or 16.
func (api ModelAPI) AppPlaces() int {
	if api.AppParallel > 0 {
		return api.AppParallel
	}
	return 16
}

// Chat names the model and effort of the chat of the site: the model and model_reasoning_effort of its overrides of
// Codex, the one place they are set; empty when the overrides do not set them.
func (config Config) Chat() (model, effort string) {
	return overrideString(config.CodexConfigOverrides, "model"), overrideString(config.CodexConfigOverrides, "model_reasoning_effort")
}

// ChatWindow names the window of the context of the chat of the site and its threshold of compaction: the
// model_context_window and model_auto_compact_token_limit of its overrides of Codex, the one place they are set, read as
// Chat reads the model (specs/2026-11-13-desk-account-ai.md AI5.1); zero when the overrides do not set them.
func (config Config) ChatWindow() (window, compact int64) {
	return overrideInt(config.CodexConfigOverrides, "model_context_window"), overrideInt(config.CodexConfigOverrides, "model_auto_compact_token_limit")
}

// overrideInt is the whole value of a key of the overrides of Codex, key=123; zero when absent or not a whole number.
func overrideInt(overrides []string, key string) int64 {
	for _, override := range overrides {
		name, value, found := strings.Cut(override, "=")
		if !found || strings.TrimSpace(name) != key {
			continue
		}
		number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || number < 0 {
			return 0
		}
		return number
	}
	return 0
}

// overrideString is the string value of a key of the overrides of Codex, key="value"; empty when absent or not a string.
func overrideString(overrides []string, key string) string {
	for _, override := range overrides {
		name, value, found := strings.Cut(override, "=")
		if !found || strings.TrimSpace(name) != key {
			continue
		}
		unquoted, err := strconv.Unquote(strings.TrimSpace(value))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(unquoted)
	}
	return ""
}

// Voice names the model of the voice: VoiceModel, or gpt-live-1.1-codex when none is set.
func (api ModelAPI) Voice() string {
	if api.VoiceModel == "" {
		return "gpt-live-1.1-codex"
	}
	return api.VoiceModel
}

// Brain is the model and effort of the voice's assistant (V4.3).
func (api ModelAPI) Brain() (model, effort string) {
	model, effort = api.VoiceBrain, api.VoiceBrainEffort
	if model == "" {
		model = "gpt-6.1-sol"
	}
	if effort == "" {
		effort = "low"
	}
	return model, effort
}

func splitAllowedOrigins(raw string) []string {
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, part := range parts {
		if origin := strings.TrimSpace(part); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func secureCookiesDefault(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "development", "local", "test":
		return false
	default:
		return true
	}
}
