package clientruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestClaudeMCPKeepsOtherFieldsAndUserServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	original := `{"numStartups":7,"projects":{"/x":{"allowedTools":["a"]}},"mcpServers":{"mine":{"type":"stdio","command":"my-tool","args":[]}}}`
	if err := os.WriteFile(claudeJSONPath(), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := SaveMCP("claude", MCPServer{Name: "fs", Type: "stdio", Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "C:\\Users\\a b"}, Env: map[string]string{"TOKEN": "s3cret"}})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, s := range list {
		if s.Name == "fs" {
			id = s.ID
		}
		if s.Name == "mine" && !s.External {
			t.Fatal("user server not marked external")
		}
	}
	if list, err = SetMCPEnabled("claude", id, true); err != nil {
		t.Fatal(err)
	}
	v := readJSON(t, claudeJSONPath())
	if v["numStartups"].(float64) != 7 || v["projects"] == nil {
		t.Fatalf("other fields lost: %v", v)
	}
	servers := v["mcpServers"].(map[string]any)
	if servers["mine"] == nil || servers["fs"] == nil {
		t.Fatalf("servers = %v", servers)
	}
	if got := servers["fs"].(map[string]any)["env"].(map[string]any)["TOKEN"]; got != "s3cret" {
		t.Fatalf("env = %v", got)
	}
	if _, err := os.Stat(claudeJSONPath() + ".jieshangai-mcp.bak"); err != nil {
		t.Fatal("no backup of the original file")
	}
	// The UI state never carries env values.
	cacheMCP("claude", list)
	for _, s := range mcpSnapshot()["claude"] {
		for _, val := range s.Env {
			if val != "" {
				t.Fatal("env value leaked into state")
			}
		}
	}
	// Editing with a blank env value keeps the stored secret.
	if _, err := SaveMCP("claude", MCPServer{ID: id, Name: "fs", Type: "stdio", Command: "npx", Args: []string{"-y"}, Env: map[string]string{"TOKEN": ""}}); err != nil {
		t.Fatal(err)
	}
	servers = readJSON(t, claudeJSONPath())["mcpServers"].(map[string]any)
	if got := servers["fs"].(map[string]any)["env"].(map[string]any)["TOKEN"]; got != "s3cret" {
		t.Fatalf("secret lost on edit: %v", got)
	}
	// A name the user already uses is refused, not overwritten.
	if _, err := SaveMCP("claude", MCPServer{Name: "mine", Type: "http", URL: "https://x"}); err == nil {
		t.Fatal("accepted a name that clashes with a user server")
	}
	if err := clearMCP(); err != nil {
		t.Fatal(err)
	}
	servers = readJSON(t, claudeJSONPath())["mcpServers"].(map[string]any)
	if servers["fs"] != nil || servers["mine"] == nil {
		t.Fatalf("restore removed the wrong servers: %v", servers)
	}
	if list, _ := ListMCP("claude"); len(list) != 2 {
		t.Fatalf("stored list should survive restore: %+v", list)
	}
}

func TestCodexMCPUsesItsOwnBlock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	cfg := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "model_provider = \"jieshangai\"\n\n[mcp_servers.mine]\ncommand = \"x\"\n\n# jieshangai-provider-start\n[model_providers.jieshangai]\nname = \"接上AI\"\n# jieshangai-provider-end\n"
	if err := os.WriteFile(cfg, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := SaveMCP("codex", MCPServer{Name: "web", Type: "stdio", Command: "uvx", Args: []string{`say "hi"`}, Env: map[string]string{"K": "v"}})
	if err != nil {
		t.Fatal(err)
	}
	id := list[0].ID
	if _, err := SetMCPEnabled("codex", id, true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(cfg)
	text := string(got)
	for _, want := range []string{"[mcp_servers.mine]", "# jieshangai-provider-start", mcpBlockStart, "[mcp_servers.web]", `args = ["say \"hi\""]`, "[mcp_servers.web.env]", `K = "v"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	list, _ = ListMCP("codex")
	if !list[0].Enabled {
		t.Fatal("enabled state not read back from config.toml")
	}
	if _, err := SaveMCP("codex", MCPServer{Name: "mine", Type: "http", URL: "https://x"}); err == nil {
		t.Fatal("accepted a name that clashes with a user server")
	}
	if _, err := DeleteMCP("codex", id); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(cfg)
	if strings.Contains(string(got), "mcp_servers.web") || strings.Contains(string(got), mcpBlockStart) {
		t.Fatalf("block left behind:\n%s", got)
	}
	if !strings.Contains(string(got), "[mcp_servers.mine]") || !strings.Contains(string(got), "# jieshangai-provider-end") {
		t.Fatalf("user content lost:\n%s", got)
	}
}

func TestMCPValidation(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	bad := []MCPServer{
		{Name: "has space", Type: "stdio", Command: "x"},
		{Name: "a.b", Type: "stdio", Command: "x"},
		{Name: "ok", Type: "stdio"},
		{Name: "ok", Type: "http", URL: "file:///etc/passwd"},
		{Name: "ok", Type: "stdio", Command: "x", Env: map[string]string{"BAD-KEY": "v"}},
		{Name: "ok", Type: "shell", Command: "x"},
	}
	for _, s := range bad {
		if _, err := SaveMCP("claude", s); err == nil {
			t.Fatalf("accepted %+v", s)
		}
	}
	if _, err := SaveMCP("claude", MCPServer{Name: "remote", Type: "sse", URL: "https://mcp.example.com/sse"}); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeMCPRefusesBrokenConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	if err := os.WriteFile(claudeJSONPath(), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := SaveMCP("claude", MCPServer{Name: "fs", Type: "http", URL: "https://x"})
	if err == nil {
		_, err = SetMCPEnabled("claude", list[0].ID, true)
	}
	if err == nil {
		t.Fatal("wrote into a broken ~/.claude.json")
	}
	got, _ := os.ReadFile(claudeJSONPath())
	if string(got) != "{broken" {
		t.Fatal("broken file was changed")
	}
}
