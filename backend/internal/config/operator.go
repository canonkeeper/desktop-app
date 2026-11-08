package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Operator is what the operator command takes from backend.json: the product database, the
// platform administrator, the backend's operator socket and the state directory that holds
// the file (specs/2026-10-28-account-data-rights.md, R1.3).
type Operator struct {
	Environment         string
	DatabaseURL         string
	PlatformAdminUserID string
	OperatorSocket      string
	StateDir            string
	// ModelAPI is the model endpoint the chapter-cards build calls; the key is read from its
	// variable at call time (specs/2026-10-29-chapter-cards-quality.md, UC1).
	ModelAPI ModelAPI
}

// LoadOperatorFile reads backend.json as strictly as LoadFile but leaves the secrets it does
// not need (the SMTP key file) unread.
func LoadOperatorFile(path string) (Operator, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Operator{}, errors.New("config: invalid operator config path")
	}
	file, err := os.Open(absolute)
	if err != nil {
		return Operator{}, fmt.Errorf("config: open file: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var input fileConfig
	if err := decoder.Decode(&input); err != nil {
		return Operator{}, fmt.Errorf("config: decode file: %w", err)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return Operator{}, err
	}
	if err := input.validate(); err != nil {
		return Operator{}, err
	}
	operator := Operator{
		Environment:         strings.TrimSpace(input.Environment),
		DatabaseURL:         strings.TrimSpace(input.DatabaseURL),
		PlatformAdminUserID: strings.TrimSpace(input.PlatformAdminUserID),
		OperatorSocket:      strings.TrimSpace(input.OperatorSocket),
		StateDir:            filepath.Dir(absolute),
		ModelAPI:            input.ModelAPI.modelAPI(),
	}
	if operator.OperatorSocket != "" && !filepath.IsAbs(operator.OperatorSocket) {
		return Operator{}, errors.New("config: operator socket path must be absolute")
	}
	return operator, nil
}
