// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io/fs"
	"time"
)

// The package of the extension of Claude Desktop for Windows (specs/2026-11-15-desk-mcp.md MC3.3): an MCPB of the manifest
// 0.3 with the icon of the app and a copy of the program of the bridge — a package of the kind binary carries its program,
// and an update of CanonKeeper does not touch the copy; the copy says «установите расширение заново» when its set differs.
const (
	extensionProgram = "server/canonkeeper-mcp.exe"
	extensionIcon    = "icon.png"
	// extensionAuthor is the author of the package: the same in every version, so Claude Desktop knows it for one extension.
	extensionAuthor = "CanonKeeper"
)

// ExtensionParts are what the package is built of: the version of the app, the data directory the copy reads mcp.json in,
// the program of the bridge, the icon of the app, the tools of the set for the window of the installation and the word of
// the copy of the extension the program takes after the data directory.
type ExtensionParts struct {
	Version string
	DataDir string
	Program []byte
	Icon    []byte
	Tools   []ExtensionTool
	Kind    string
}

// ExtensionTool is a tool as the window of the installation lists it.
type ExtensionTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ExtensionName is the name of the file: CanonKeeper-<version>.mcpb.
func ExtensionName(version string) string { return "CanonKeeper-" + version + ".mcpb" }

type extensionManifest struct {
	ManifestVersion string             `json:"manifest_version"`
	Name            string             `json:"name"`
	DisplayName     string             `json:"display_name"`
	Version         string             `json:"version"`
	Description     string             `json:"description"`
	Author          extensionAuthorOf  `json:"author"`
	Icon            string             `json:"icon"`
	Server          extensionServer    `json:"server"`
	Tools           []ExtensionTool    `json:"tools"`
	Compatibility   extensionPlatforms `json:"compatibility"`
}

type extensionAuthorOf struct {
	Name string `json:"name"`
}

type extensionServer struct {
	Type       string          `json:"type"`
	EntryPoint string          `json:"entry_point"`
	MCPConfig  extensionConfig `json:"mcp_config"`
}

type extensionConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type extensionPlatforms struct {
	Platforms []string `json:"platforms"`
}

// Extension is the package: manifest.json, the icon and the copy of the program, run as ${__dirname} and its path with .exe,
// with the data directory and the word extension; no env and no user_config.
func Extension(parts ExtensionParts) ([]byte, error) {
	manifest := extensionManifest{
		ManifestVersion: "0.3", Name: "canonkeeper", DisplayName: "CanonKeeper", Version: parts.Version,
		Description: "Ваши саги из CanonKeeper: поиск по тексту, главы, имена, хронология и то, что вы просили помнить. Читать можно, пока CanonKeeper открыт.",
		Author:      extensionAuthorOf{Name: extensionAuthor}, Icon: extensionIcon,
		Server: extensionServer{Type: "binary", EntryPoint: extensionProgram, MCPConfig: extensionConfig{
			Command: "${__dirname}/" + extensionProgram, Args: []string{parts.DataDir, parts.Kind},
		}},
		Compatibility: extensionPlatforms{Platforms: []string{"win32"}},
	}
	for _, tool := range parts.Tools {
		manifest.Tools = append(manifest.Tools, ExtensionTool{Name: tool.Name, Description: firstLine(tool.Description)})
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	for _, file := range []struct {
		name    string
		content []byte
		mode    uint32
	}{{"manifest.json", encoded, 0o644}, {extensionIcon, parts.Icon, 0o644}, {extensionProgram, parts.Program, 0o755}} {
		header := &zip.FileHeader{Name: file.name, Method: zip.Deflate, Modified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		header.SetMode(fs.FileMode(file.mode))
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write(file.content); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// firstLine is the description of a tool for the window of the installation: its first sentence of the chat.
func firstLine(description string) string {
	for index, letter := range description {
		if letter == '.' || letter == '\n' {
			return description[:index+1]
		}
	}
	return description
}
