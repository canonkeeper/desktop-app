package agent

import "testing"

func TestRuntimeSettingsValidation(t *testing.T) {
	if !ValidConfigOverrides([]string{`model="gpt-6-luna"`, `model_providers.codex-lb.base_url="https://x/y"`}) {
		t.Fatal("valid overrides rejected")
	}
	for _, override := range []string{"model", "=x", "model=", " model=\"x\"", "model=\"x\"\nshell=true"} {
		if ValidConfigOverrides([]string{override}) {
			t.Errorf("override %q accepted", override)
		}
	}
	if !ValidPassEnv([]string{"CODEX_LB_API_KEY"}) {
		t.Fatal("valid variable name rejected")
	}
	for _, name := range []string{"HOME", "PATH", "CODEX_HOME", "codex_key", "1KEY", ""} {
		if ValidPassEnv([]string{name}) {
			t.Errorf("variable %q accepted", name)
		}
	}
}
