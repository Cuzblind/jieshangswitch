package clientruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func enableMCP(t *testing.T, agent string, s MCPServer) []MCPServer {
	t.Helper()
	list, err := SaveMCP(agent, s)
	if err != nil {
		t.Fatalf("save %s: %v", s.Name, err)
	}
	id := ""
	for _, x := range list {
		if x.Name == s.Name {
			id = x.ID
		}
	}
	if list, err = SetMCPEnabled(agent, id, true); err != nil {
		t.Fatalf("enable %s: %v", s.Name, err)
	}
	return list
}

// Claude Code reads stdio, http and sse servers, each in its own shape.
func TestClaudeMCPWritesAllThreeTransports(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	enableMCP(t, "claude", MCPServer{Name: "files", Type: "stdio", Command: "npx", Args: []string{"-y", "fs"}, Cwd: `C:\work`})
	enableMCP(t, "claude", MCPServer{Name: "api", Type: "http", URL: "https://mcp.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer h1"}})
	list := enableMCP(t, "claude", MCPServer{Name: "events", Type: "sse", URL: "https://mcp.example.com/sse", Headers: map[string]string{"X-Api-Key": "k2"}})
	servers := readJSON(t, claudeJSONPath())["mcpServers"].(map[string]any)
	files := servers["files"].(map[string]any)
	if files["type"] != "stdio" || files["cwd"] != `C:\work` {
		t.Fatalf("stdio entry = %v", files)
	}
	api := servers["api"].(map[string]any)
	if api["type"] != "http" || api["headers"].(map[string]any)["Authorization"] != "Bearer h1" {
		t.Fatalf("http entry = %v", api)
	}
	events := servers["events"].(map[string]any)
	if events["type"] != "sse" || events["url"] != "https://mcp.example.com/sse" || events["headers"].(map[string]any)["X-Api-Key"] != "k2" {
		t.Fatalf("sse entry = %v", events)
	}
	for _, s := range list {
		if !s.External && !s.Enabled {
			t.Fatalf("%s not read back as enabled: %+v", s.Name, s)
		}
	}
}

// Codex infers the transport from url; http and sse both carry http_headers.
func TestCodexMCPWritesAllThreeTransports(t *testing.T) {
	home := t.TempDir()
	t.Setenv("RELAY_HOME", home)
	enableMCP(t, "codex", MCPServer{Name: "files", Type: "stdio", Command: "uvx", Cwd: "/srv/work"})
	enableMCP(t, "codex", MCPServer{Name: "api", Type: "http", URL: "https://mcp.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer h1"}})
	list := enableMCP(t, "codex", MCPServer{Name: "events", Type: "sse", URL: "https://mcp.example.com/sse", Headers: map[string]string{"X-Api-Key": "k2"}})
	data, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	text := string(data)
	for _, want := range []string{
		"[mcp_servers.files]", `cwd = "/srv/work"`,
		"[mcp_servers.api]", "[mcp_servers.api.http_headers]", `Authorization = "Bearer h1"`,
		"[mcp_servers.events]", `url = "https://mcp.example.com/sse"`, "[mcp_servers.events.http_headers]", `X-Api-Key = "k2"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	for _, s := range list {
		if !s.Enabled {
			t.Fatalf("%s not enabled: %+v", s.Name, s)
		}
	}
}

// Header values are usually tokens: hidden from the page, kept on a blank edit.
func TestMCPHeadersAreMaskedAndKept(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	list := enableMCP(t, "claude", MCPServer{Name: "api", Type: "http", URL: "https://mcp.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer secret"}})
	cacheMCP("claude", list)
	id := ""
	for _, s := range mcpSnapshot()["claude"] {
		if s.Name == "api" {
			id = s.ID
			if v := s.Headers["Authorization"]; v != "" {
				t.Fatalf("header value leaked into state: %q", v)
			}
		}
	}
	if _, err := SaveMCP("claude", MCPServer{ID: id, Name: "api", Type: "sse", URL: "https://mcp.example.com/sse", Headers: map[string]string{"Authorization": ""}}); err != nil {
		t.Fatal(err)
	}
	api := readJSON(t, claudeJSONPath())["mcpServers"].(map[string]any)["api"].(map[string]any)
	if api["type"] != "sse" || api["headers"].(map[string]any)["Authorization"] != "Bearer secret" {
		t.Fatalf("edit lost the header or type: %v", api)
	}
}

func TestMCPTransportValidation(t *testing.T) {
	t.Setenv("RELAY_HOME", t.TempDir())
	for _, s := range []MCPServer{
		{Name: "a", Type: "sse", URL: "file:///etc/passwd"},
		{Name: "b", Type: "http", URL: "https://x", Headers: map[string]string{"Bad Header": "v"}},
		{Name: "c", Type: "sse", URL: "https://x", Headers: map[string]string{"X-Key": "line\nbreak"}},
		{Name: "d", Type: "stdio", Command: "x", Cwd: "bad\x00dir"},
	} {
		if _, err := SaveMCP("claude", s); err == nil {
			t.Fatalf("accepted %+v", s)
		}
	}
}
