package agent

import (
	"regexp"
	"strings"
)

var (
	configOverrideKey = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	environmentName   = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	fixedEnvironment  = map[string]bool{
		"CODEX_HOME": true, "CODEX_SQLITE_HOME": true, "HOME": true, "LANG": true, "LC_ALL": true,
		"NO_PROXY": true, "PATH": true, "TMPDIR": true, "TZ": true, "CODEX_CODE_MODE_HOST_PATH": true,
	}
)

// ValidConfigOverrides accepts single-line Codex `key=value` settings with a dotted key;
// they configure the model and provider per environment.
func ValidConfigOverrides(overrides []string) bool {
	for _, override := range overrides {
		key, value, found := strings.Cut(override, "=")
		if !found || !configOverrideKey.MatchString(key) || strings.TrimSpace(value) == "" || strings.ContainsAny(override, "\r\n") {
			return false
		}
	}
	return true
}

// ValidPassEnv accepts upper-case environment variable names that do not replace the
// fixed app-server environment.
func ValidPassEnv(names []string) bool {
	for _, name := range names {
		if !environmentName.MatchString(name) || fixedEnvironment[name] {
			return false
		}
	}
	return true
}
