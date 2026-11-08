package codex

import (
	"runtime/debug"
	"testing"
)

// specs/2026-10-29-admin-panel.md R5.4: the required SDK version and the replacing fork.
func TestSDKBuildNamesTheReplacedFork(t *testing.T) {
	info := &debug.BuildInfo{Deps: []*debug.Module{
		{Path: "github.com/go-chi/chi/v5", Version: "v5.2.3"},
		{Path: sdkModule, Version: "v0.153.4", Replace: &debug.Module{Path: "github.com/Dirard/codex/sdk/go", Version: "v0.0.0-20260905163411-20a8b8519f4d"}},
	}}
	version, replacement := sdkBuild(info)
	if version != "v0.153.4" || replacement != "github.com/Dirard/codex/sdk/go v0.0.0-20260905163411-20a8b8519f4d" {
		t.Fatalf("build = %q, %q", version, replacement)
	}
	info.Deps[1].Replace = nil
	if version, replacement := sdkBuild(info); version != "v0.153.4" || replacement != "" {
		t.Fatalf("unreplaced build = %q, %q", version, replacement)
	}
	if version, replacement := sdkBuild(&debug.BuildInfo{}); version != "" || replacement != "" {
		t.Fatalf("no module = %q, %q", version, replacement)
	}
}
