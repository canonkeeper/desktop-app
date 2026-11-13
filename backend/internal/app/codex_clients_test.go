package app

import (
	"path/filepath"
	"testing"

	codexadapter "canonkeeper/backend/internal/adapters/codex"
	"canonkeeper/backend/internal/config"
	agentdomain "canonkeeper/backend/internal/domain/agent"
)

func TestCodexClientConfigUsesDistinctFlowHomes(t *testing.T) {
	cfg := config.Config{
		CodexPath: "/opt/codex", CodexCodeModeHost: "/opt/code-mode", CodexCWD: "/srv/work",
		CodexRepositoryHome: "/srv/codex-homes", CodexHome: "/var/lib/codex-home",
		CodexSQLiteHome: "/var/lib/codex-sqlite", CodexPrivateHome: "/var/lib/codex-private", CodexTempDir: "/var/lib/codex-tmp",
	}
	for _, flow := range agentdomain.Flows() {
		got, err := codexClientConfig(cfg, flow)
		if err != nil {
			t.Fatalf("codexClientConfig(%q): %v", flow, err)
		}
		if got.CodexPath != cfg.CodexPath || got.CodeModeHostPath != cfg.CodexCodeModeHost || got.CWD != cfg.CodexCWD ||
			got.RepositoryHome != filepath.Join(cfg.CodexRepositoryHome, string(flow)) ||
			got.CodexHome != filepath.Join(cfg.CodexHome, string(flow)) ||
			got.CodexSQLiteHome != filepath.Join(cfg.CodexSQLiteHome, string(flow)) ||
			got.PrivateHome != filepath.Join(cfg.CodexPrivateHome, string(flow)) ||
			got.TempDir != filepath.Join(cfg.CodexTempDir, string(flow)) {
			t.Fatalf("config for %q = %#v", flow, got)
		}
	}
	for _, flow := range []agentdomain.Flow{"indexing", "unknown"} {
		if _, err := codexClientConfig(cfg, flow); err == nil {
			t.Fatalf("%s flow config succeeded", flow)
		}
	}
}

// specs/2026-11-02-image-workshop.md IW4.1: every flow has its own app-server; a run, a thread read, a cancellation
// and a deletion go to the app-server of their flow.
func TestCodexClientsSelectExactFlowAndCloseZeroValue(t *testing.T) {
	research, workshop, writer := &codexadapter.Client{}, &codexadapter.Client{}, &codexadapter.Client{}
	clients := CodexClients{}
	for flow, client := range map[agentdomain.Flow]*codexadapter.Client{
		agentdomain.FlowResearchChat: research, agentdomain.FlowImageWorkshop: workshop, agentdomain.FlowWriterWorkshop: writer,
	} {
		if err := clients.set(flow, client); err != nil {
			t.Fatal(err)
		}
	}
	if err := clients.set("indexing", research); err == nil {
		t.Fatal("a client of an unknown flow was kept")
	}
	if got, err := clients.Client(agentdomain.FlowResearchChat); err != nil || got != research {
		t.Fatalf("Client(research chat) = (%p, %v), want %p", got, err, research)
	}
	if got, err := clients.Client(agentdomain.FlowImageWorkshop); err != nil || got != workshop {
		t.Fatalf("Client(image workshop) = (%p, %v), want %p", got, err, workshop)
	}
	if got, err := clients.Client(agentdomain.FlowWriterWorkshop); err != nil || got != writer {
		t.Fatalf("Client(writer workshop) = (%p, %v), want %p", got, err, writer)
	}
	for _, flow := range []agentdomain.Flow{"indexing", "unknown"} {
		if _, err := clients.Client(flow); err == nil {
			t.Fatalf("%s flow client succeeded", flow)
		}
	}
	if err := (CodexClients{}).Close(); err != nil {
		t.Fatalf("zero close: %v", err)
	}
}

// specs/2026-11-13-desk-home.md HM1.1: the build names the way of the home — the client of every flow copies the profile
// when the configuration says so — and the configuration of the site, read from the environment, links it.
func TestCodexClientConfigCopiesTheHomeOnlyWhereTheBuildSaysSo(t *testing.T) {
	cfg := config.Config{
		CodexPath: "/opt/codex", CodexCWD: "/srv/work", CodexRepositoryHome: "/srv/codex-homes", CodexHome: "/var/lib/codex-home",
		CodexSQLiteHome: "/var/lib/codex-sqlite", CodexPrivateHome: "/var/lib/codex-private", CodexTempDir: "/var/lib/codex-tmp",
	}
	for _, copies := range []bool{false, true} {
		cfg.CodexHomeCopies = copies
		for _, flow := range agentdomain.Flows() {
			if got, err := codexClientConfig(cfg, flow); err != nil || got.HomeCopies != copies {
				t.Fatalf("codexClientConfig(%q) with copies %v = %v, %v", flow, copies, got.HomeCopies, err)
			}
		}
	}
	if config.Load().CodexHomeCopies {
		t.Fatal("the configuration of the site copies the home")
	}
}
