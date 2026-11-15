// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"context"
	"errors"

	domain "canonkeeper/backend/internal/domain/desktop"
)

// ErrNoExtension is a computer without the extension of Claude Desktop: not Windows, or the shell named no program of the
// bridge.
var ErrNoExtension = errors.New("desktop: there is no extension of Claude Desktop here")

// ClaudeDesktopExtension is getClaudeDesktopExtension (specs/2026-11-15-desk-mcp.md MC3.3): the package the server builds
// on every call — its manifest, the icon of the app and a copy of the program of the bridge run with the data directory.
type ClaudeDesktopExtension struct {
	Bridge  *domain.Bridge
	DataDir string
	Version string
	Icon    []byte
	Windows bool
	// Tools are the tools of the set for the window of the installation; Kind is the word of the copy of the extension.
	Tools []domain.ExtensionTool
	Kind  string
	// Read reads the program of the bridge whole.
	Read func(path string) ([]byte, error)
}

// Execute is the name of the file and its content.
func (extension ClaudeDesktopExtension) Execute(context.Context) (string, []byte, error) {
	if !extension.Windows || extension.Bridge == nil {
		return "", nil, ErrNoExtension
	}
	program, err := extension.Read(extension.Bridge.Program)
	if err != nil {
		return "", nil, err
	}
	content, err := domain.Extension(domain.ExtensionParts{
		Version: extension.Version, DataDir: extension.DataDir, Program: program, Icon: extension.Icon, Tools: extension.Tools, Kind: extension.Kind,
	})
	if err != nil {
		return "", nil, err
	}
	return domain.ExtensionName(extension.Version), content, nil
}
