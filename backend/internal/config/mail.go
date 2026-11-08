package config

import (
	"errors"
	"io"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Mail configures account letters (UC4.4): SMTP in production, a directory outbox
// in TEST and development. The sender address and the SMTP login come only from
// configuration; the SMTP password comes from a private file read once at start.
type Mail struct {
	Mode      string
	PublicURL string
	From      string
	OutboxDir string
	SMTP      MailSMTP
}

type MailSMTP struct {
	Host         string
	Port         int
	Username     string
	PasswordFile string
	Password     Secret
}

const (
	MailModeSMTP   = "smtp"
	MailModeOutbox = "outbox"
	// maxPasswordFileBytes bounds the read of the password file.
	maxPasswordFileBytes = 4096
)

// Secret is a value that never prints.
type Secret string

func (Secret) String() string   { return "[hidden]" }
func (Secret) GoString() string { return "[hidden]" }

type fileMail struct {
	Mode      string   `json:"mode"`
	PublicURL string   `json:"public_url"`
	From      string   `json:"from"`
	OutboxDir string   `json:"outbox_dir"`
	SMTP      fileSMTP `json:"smtp"`
}

type fileSMTP struct {
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Username     string `json:"username"`
	PasswordFile string `json:"password_file"`
}

// loadFileMail maps the mail section of backend.json and reads the SMTP password.
func loadFileMail(input *fileMail, environment string) (Mail, error) {
	if input == nil {
		return Mail{}, errors.New("config: file requires the mail section")
	}
	config := Mail{
		Mode: strings.TrimSpace(input.Mode), PublicURL: strings.TrimSpace(input.PublicURL),
		From: strings.TrimSpace(input.From), OutboxDir: strings.TrimSpace(input.OutboxDir),
		SMTP: MailSMTP{
			Host: strings.TrimSpace(input.SMTP.Host), Port: input.SMTP.Port,
			Username: strings.TrimSpace(input.SMTP.Username), PasswordFile: strings.TrimSpace(input.SMTP.PasswordFile),
		},
	}
	if config.Mode == MailModeOutbox && !filepath.IsAbs(config.OutboxDir) {
		return Mail{}, errors.New("config: mail outbox directory must be absolute")
	}
	if config.Mode == MailModeSMTP {
		if !filepath.IsAbs(config.SMTP.PasswordFile) {
			return Mail{}, errors.New("config: SMTP password file must be an absolute path")
		}
		password, err := readPasswordFile(config.SMTP.PasswordFile)
		if err != nil {
			return Mail{}, err
		}
		config.SMTP.Password = password
	}
	return config, config.validate(environment)
}

// readPasswordFile reads a regular file without group or other access and drops
// one trailing line break. Errors never carry the content.
func readPasswordFile(path string) (Secret, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", errors.New("config: SMTP password file is unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("config: SMTP password file must be a regular file readable only by its owner")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("config: SMTP password file is unavailable")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxPasswordFileBytes+1))
	if err != nil || len(raw) > maxPasswordFileBytes {
		return "", errors.New("config: SMTP password file is unreadable")
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("config: SMTP password file must hold one non-empty line")
	}
	return Secret(value), nil
}

func (config Mail) validate(environment string) error {
	production := strings.EqualFold(strings.TrimSpace(environment), "production")
	switch config.Mode {
	case MailModeSMTP, MailModeOutbox:
	default:
		return errors.New("config: mail mode must be smtp or outbox")
	}
	if production && config.Mode != MailModeSMTP {
		return errors.New("config: production mail must use SMTP")
	}
	parsed, err := url.Parse(config.PublicURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil ||
		strings.HasSuffix(config.PublicURL, "?") || strings.HasSuffix(config.PublicURL, "#") {
		return errors.New("config: mail public URL must be an absolute origin")
	}
	if production && parsed.Scheme != "https" {
		return errors.New("config: production mail public URL must use https")
	}
	if address, err := mail.ParseAddress(config.From); err != nil || address.Name != "" || address.Address != config.From {
		return errors.New("config: mail sender must be a bare address")
	}
	if config.Mode == MailModeOutbox && config.OutboxDir == "" {
		return errors.New("config: mail outbox requires a directory")
	}
	if config.Mode == MailModeSMTP {
		smtp := config.SMTP
		if smtp.Host == "" || smtp.Port != 465 {
			return errors.New("config: SMTP requires a host and port 465 (implicit TLS)")
		}
		if smtp.Username != config.From {
			return errors.New("config: SMTP login must equal the mail sender")
		}
		if smtp.Password == "" {
			return errors.New("config: SMTP requires a password")
		}
	}
	return nil
}
