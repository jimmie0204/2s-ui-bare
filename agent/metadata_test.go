package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMetadataAddress(t *testing.T) {
	address, ok := metadataAddress(map[string]any{
		"server": "ora.example.com",
		"port":   9443,
		"sni":    "ora.example.com",
	})
	if !ok || address["server"] != "ora.example.com" || address["server_port"] != 9443 {
		t.Fatalf("address=%v ok=%v", address, ok)
	}
	tls, ok := address["tls"].(map[string]any)
	if !ok || tls["server_name"] != "ora.example.com" {
		t.Fatalf("tls=%v", address["tls"])
	}
}

func TestLoadMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.yaml")
	if err := os.WriteFile(path, []byte("inbounds:\n  hy2-in:\n    server: ora.example.com\n    port: 9443\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &Agent{opts: AgentOptions{MetadataFile: path}}
	metadata, err := a.loadMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Inbounds["hy2-in"]["server"] != "ora.example.com" {
		t.Fatalf("metadata=%v", metadata)
	}
}
