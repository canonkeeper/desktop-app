package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type fileConfig struct {
	HTTPAddr            string   `json:"http_addr"`
	Environment         string   `json:"environment"`
	DatabaseURL         string   `json:"database_url"`
	WorkerDatabaseURL   string   `json:"worker_database_url"`
	WorkersEnabled      bool     `json:"workers_enabled"`
	AllowedOrigins      []string `json:"allowed_origins"`
	SecureCookies       bool     `json:"secure_cookies"`
	FileStorageDir      string   `json:"file_storage_dir"`
	PlatformAdminUserID string   `json:"platform_admin_user_id"`
	// PromoOperatorUserIDs are the operator's other accounts: their sagas count as the
	// administrator's own for the permissions for promotion.
	PromoOperatorUserIDs []string          `json:"promo_operator_user_ids"`
	Codex                fileCodex         `json:"codex"`
	ModelAPI             fileModelAPI      `json:"model_api"`
	Mail                 *fileMail         `json:"mail"`
	OperatorSocket       string            `json:"operator_socket"`
	Illustrations        fileIllustrations `json:"illustrations"`
}

// fileIllustrations are the switches of the saga gallery; an absent section keeps them off.
type fileIllustrations struct {
	UploadsEnabled bool `json:"uploads_enabled"`
}

type fileModelAPI struct {
	BaseURL      string `json:"base_url"`
	KeyEnv       string `json:"key_env"`
	Model        string `json:"model"`
	Effort       string `json:"effort"`
	CardsModel   string `json:"cards_model"`
	CardsEffort  string `json:"cards_effort"`
	ImageModel   string `json:"image_model"`
	ImageQuality string `json:"image_quality"`
	CheckModel   string `json:"check_model"`
	Parallel     int    `json:"parallel"`
	// WritingModel, WritingEffort and WritingCheckModel are the writing agent (specs/2026-11-06-writing-agent.md, WA5.11).
	WritingModel      string `json:"writing_model"`
	WritingEffort     string `json:"writing_effort"`
	WritingCheckModel string `json:"writing_check_model"`
	// VoiceURL and VoiceModel are the voice of specs/2026-11-03-voice-assistant.md V4.1; VoiceBrain and
	// VoiceBrainEffort its assistant (V4.3).
	VoiceURL         string `json:"voice_url"`
	VoiceModel       string `json:"voice_model"`
	VoiceBrain       string `json:"voice_brain"`
	VoiceBrainEffort string `json:"voice_brain_effort"`
}

func (input fileModelAPI) modelAPI() ModelAPI {
	return ModelAPI{
		BaseURL: strings.TrimSpace(input.BaseURL), KeyEnv: strings.TrimSpace(input.KeyEnv),
		Model: strings.TrimSpace(input.Model), Effort: strings.TrimSpace(input.Effort),
		CardsModel: strings.TrimSpace(input.CardsModel), CardsEffort: strings.TrimSpace(input.CardsEffort),
		ImageModel: strings.TrimSpace(input.ImageModel), ImageQuality: strings.TrimSpace(input.ImageQuality),
		CheckModel: strings.TrimSpace(input.CheckModel), Parallel: input.Parallel,
		WritingModel: strings.TrimSpace(input.WritingModel), WritingEffort: strings.TrimSpace(input.WritingEffort),
		WritingCheckModel: strings.TrimSpace(input.WritingCheckModel),
		VoiceURL:          strings.TrimSpace(input.VoiceURL), VoiceModel: strings.TrimSpace(input.VoiceModel),
		VoiceBrain: strings.TrimSpace(input.VoiceBrain), VoiceBrainEffort: strings.TrimSpace(input.VoiceBrainEffort),
	}
}

type fileCodex struct {
	Path            string   `json:"path"`
	CodeModeHost    string   `json:"code_mode_host_path"`
	CWD             string   `json:"cwd"`
	RepositoryHome  string   `json:"repository_home"`
	Home            string   `json:"home"`
	SQLiteHome      string   `json:"sqlite_home"`
	PrivateHome     string   `json:"private_home"`
	TempDir         string   `json:"temp_dir"`
	ConfigOverrides []string `json:"config_overrides"`
	PassEnv         []string `json:"pass_env"`
}

// LoadFile читает только указанный JSON-файл и не обращается к BACKEND_*.
func LoadFile(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: open file: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var input fileConfig
	if err := decoder.Decode(&input); err != nil {
		return Config{}, fmt.Errorf("config: decode file: %w", err)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return Config{}, err
	}
	if err := input.validate(); err != nil {
		return Config{}, err
	}
	mailConfig, err := loadFileMail(input.Mail, input.Environment)
	if err != nil {
		return Config{}, err
	}
	return newConfig(settings{
		HTTPAddr: input.HTTPAddr, Environment: input.Environment, DatabaseURL: input.DatabaseURL,
		WorkerDatabaseURL: input.WorkerDatabaseURL, WorkersEnabled: input.WorkersEnabled,
		AllowedOrigins: input.AllowedOrigins, SecureCookies: input.SecureCookies, FileStorageDir: input.FileStorageDir,
		PlatformAdminUserID: input.PlatformAdminUserID, PromoOperatorUserIDs: input.PromoOperatorUserIDs,
		CodexPath: input.Codex.Path, CodexCodeModeHost: input.Codex.CodeModeHost, CodexCWD: input.Codex.CWD,
		CodexRepositoryHome: input.Codex.RepositoryHome, CodexHome: input.Codex.Home,
		CodexSQLiteHome: input.Codex.SQLiteHome, CodexPrivateHome: input.Codex.PrivateHome, CodexTempDir: input.Codex.TempDir,
		CodexConfigOverrides: input.Codex.ConfigOverrides, CodexPassEnv: input.Codex.PassEnv,
		ModelAPI:       input.ModelAPI.modelAPI(),
		Mail:           mailConfig,
		OperatorSocket: strings.TrimSpace(input.OperatorSocket),
		Illustrations:  Illustrations{UploadsEnabled: input.Illustrations.UploadsEnabled},
	}), nil
}

func (input fileConfig) validate() error {
	if strings.TrimSpace(input.HTTPAddr) == "" || strings.TrimSpace(input.Environment) == "" ||
		strings.TrimSpace(input.DatabaseURL) == "" || strings.TrimSpace(input.FileStorageDir) == "" || len(input.AllowedOrigins) == 0 {
		return errors.New("config: file requires explicit HTTP, environment, database, origins and file storage")
	}
	for _, origin := range input.AllowedOrigins {
		if strings.TrimSpace(origin) == "" || origin != strings.TrimSpace(origin) {
			return errors.New("config: file contains an invalid allowed origin")
		}
	}
	if strings.EqualFold(input.Environment, "production") && !input.SecureCookies {
		return errors.New("config: production file requires secure cookies")
	}
	return nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("config: file contains more than one JSON value")
		}
		return fmt.Errorf("config: decode file end: %w", err)
	}
	return nil
}
