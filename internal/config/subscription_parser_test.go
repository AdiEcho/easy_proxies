package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseSubscriptionContentPlainText(t *testing.T) {
	content := "vmess://node-a\n# comment\nvless://node-b\n"
	nodes, err := ParseSubscriptionContent(content)
	if err != nil {
		t.Fatalf("ParseSubscriptionContent returned error: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if nodes[0].URI != "vmess://node-a" || nodes[1].URI != "vless://node-b" {
		t.Fatalf("unexpected node URIs: %+v", nodes)
	}
}

func TestParseSubscriptionContentBase64(t *testing.T) {
	plain := "trojan://node-a\nss://node-b\n"
	content := base64.StdEncoding.EncodeToString([]byte(plain))
	nodes, err := ParseSubscriptionContent(content)
	if err != nil {
		t.Fatalf("ParseSubscriptionContent returned error: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	if nodes[0].URI != "trojan://node-a" || nodes[1].URI != "ss://node-b" {
		t.Fatalf("unexpected node URIs: %+v", nodes)
	}
}

func TestParseSubscriptionContentClashYAML(t *testing.T) {
	content := strings.TrimSpace(`
proxies:
  - name: vmess-node
    type: vmess
    server: example.com
    port: 443
    uuid: 11111111-1111-1111-1111-111111111111
`)
	nodes, err := ParseSubscriptionContent(content)
	if err != nil {
		t.Fatalf("ParseSubscriptionContent returned error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if !strings.HasPrefix(nodes[0].URI, "vmess://11111111-1111-1111-1111-111111111111@example.com:443") {
		t.Fatalf("unexpected URI: %s", nodes[0].URI)
	}
	if nodes[0].Name != "vmess-node" {
		t.Fatalf("expected node name vmess-node, got %q", nodes[0].Name)
	}
}

func TestSaveNodesPersistsEmptyInlineNodes(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	initial := Config{
		Mode: "pool",
		Listener: ListenerConfig{
			Address: "0.0.0.0",
			Port:    2323,
		},
		Nodes: []NodeConfig{
			{Name: "n1", URI: "vmess://node-a"},
		},
	}
	data, err := yaml.Marshal(&initial)
	if err != nil {
		t.Fatalf("marshal initial config: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	cfg := &Config{
		filePath: cfgPath,
		Nodes:    []NodeConfig{},
	}
	if err := cfg.SaveNodes(); err != nil {
		t.Fatalf("SaveNodes returned error: %v", err)
	}

	savedData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	var saved Config
	if err := yaml.Unmarshal(savedData, &saved); err != nil {
		t.Fatalf("unmarshal saved config: %v", err)
	}
	if len(saved.Nodes) != 0 {
		t.Fatalf("expected nodes to be empty, got %d", len(saved.Nodes))
	}
}
