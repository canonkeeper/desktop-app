package codex

import "runtime/debug"

// sdkModule is the Codex SDK module the backend requires; go.mod replaces it with the fork that
// is actually built.
const sdkModule = "github.com/openai/codex/sdk/go"

// SDKBuild names the Codex SDK this binary is built with (specs/2026-10-29-admin-panel.md R5.4):
// the required version and, when replaced, the module and version actually built. Strict
// compatibility keeps the app-server binary at the same version. Both are empty when the binary
// carries no build information.
func SDKBuild() (version, replacement string) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", ""
	}
	return sdkBuild(info)
}

func sdkBuild(info *debug.BuildInfo) (version, replacement string) {
	for _, module := range info.Deps {
		if module == nil || module.Path != sdkModule {
			continue
		}
		if module.Replace != nil {
			replacement = module.Replace.Path + " " + module.Replace.Version
		}
		return module.Version, replacement
	}
	return "", ""
}
