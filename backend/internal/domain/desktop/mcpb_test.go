// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"slices"
	"testing"
)

// specs/2026-11-15-desk-mcp.md MC3.3: the package of the extension is a zip of manifest.json — only the fields MCPB 0.3 allows
// and all it requires, by the table of testdata —, the icon of the app and the copy of the program of the bridge to the byte;
// the command is ${__dirname} with the copy and .exe, the arguments the data directory and extension; no env, no user_config.
func TestThePackageOfTheExtensionIsAnMCPB(t *testing.T) {
	var table struct {
		Allowed  []string `json:"allowed"`
		Required []string `json:"required"`
		Author   struct {
			Required, Allowed []string
		} `json:"author"`
		Server struct {
			Required, Allowed, Types []string
		} `json:"server"`
		MCPConfig struct {
			Required, Allowed []string
		} `json:"mcp_config"`
		Compatibility struct {
			Allowed, Platforms []string
		} `json:"compatibility"`
	}
	raw, err := os.ReadFile("testdata/mcpb-manifest-0.3.json")
	if err != nil || json.Unmarshal(raw, &table) != nil {
		t.Fatalf("the table: %v", err)
	}
	program := []byte("MZ the program of the bridge")
	icon := []byte("\x89PNG the icon")
	dataDir := `C:\Users\Тест\AppData\Local\ru.canonkeeper.desktop\data`
	content, err := Extension(ExtensionParts{Version: "1.4.0", DataDir: dataDir, Program: program, Icon: icon, Kind: "extension",
		Tools: []ExtensionTool{{Name: "search", Description: "Поиск по абзацам саги. Дальше."}, {Name: "sagas", Description: "Саги автора."}}})
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		files[file.Name], _ = io.ReadAll(reader)
		_ = reader.Close()
		if file.Name == "server/canonkeeper-mcp.exe" && file.Mode()&0o111 == 0 {
			t.Fatal("the program of the package does not run")
		}
	}
	if !bytes.Equal(files["server/canonkeeper-mcp.exe"], program) || !bytes.Equal(files["icon.png"], icon) || len(files) != 3 {
		t.Fatalf("the files of the package: %v", len(files))
	}
	var manifest map[string]any
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	fields := func(object map[string]any, allowed, required []string, what string) {
		t.Helper()
		for key := range object {
			if !slices.Contains(allowed, key) {
				t.Fatalf("%s has a field MCPB 0.3 does not allow: %s", what, key)
			}
		}
		for _, key := range required {
			if _, ok := object[key]; !ok {
				t.Fatalf("%s misses %s", what, key)
			}
		}
	}
	fields(manifest, table.Allowed, table.Required, "the manifest")
	fields(manifest["author"].(map[string]any), table.Author.Allowed, table.Author.Required, "author")
	server := manifest["server"].(map[string]any)
	fields(server, table.Server.Allowed, table.Server.Required, "server")
	config := server["mcp_config"].(map[string]any)
	fields(config, table.MCPConfig.Allowed, table.MCPConfig.Required, "mcp_config")
	fields(manifest["compatibility"].(map[string]any), table.Compatibility.Allowed, nil, "compatibility")
	if manifest["manifest_version"] != "0.3" || manifest["name"] != "canonkeeper" || manifest["display_name"] != "CanonKeeper" || manifest["version"] != "1.4.0" ||
		server["type"] != "binary" || server["entry_point"] != "server/canonkeeper-mcp.exe" || config["command"] != "${__dirname}/server/canonkeeper-mcp.exe" {
		t.Fatalf("the manifest: %s", files["manifest.json"])
	}
	if args := config["args"].([]any); len(args) != 2 || args[0] != dataDir || args[1] != "extension" {
		t.Fatalf("the arguments: %v", args)
	}
	if _, env := config["env"]; env {
		t.Fatal("the package has env")
	}
	if _, asks := manifest["user_config"]; asks {
		t.Fatal("the package has user_config")
	}
	if platforms := manifest["compatibility"].(map[string]any)["platforms"].([]any); len(platforms) != 1 || platforms[0] != "win32" {
		t.Fatalf("the platforms: %v", platforms)
	}
	if tools := manifest["tools"].([]any); len(tools) != 2 || tools[0].(map[string]any)["description"] != "Поиск по абзацам саги." {
		t.Fatalf("the tools: %v", tools)
	}
	if ExtensionName("1.4.0") != "CanonKeeper-1.4.0.mcpb" {
		t.Fatal("the name of the file")
	}
}
