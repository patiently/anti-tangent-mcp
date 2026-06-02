package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesDefaultsAndEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BM_URL", "http://bm.example/mcp")
	t.Setenv("BM_BEARER_TOKEN", "bm-secret")

	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("bm_username = \"alice\"\nbm_project = \"main\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := Load(cfgPath, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.BMURL != "http://bm.example/mcp" || c.BMToken != "bm-secret" {
		t.Fatalf("env not applied: %+v", c)
	}
	if c.BMUsername != "alice" || c.BMProject != "main" {
		t.Fatalf("toml not applied: %+v", c)
	}
	if c.ListenPort == 0 || c.APIToken == "" {
		t.Fatalf("defaults/token missing: %+v", c)
	}

	// client.json must be written for the extension, mode 0600
	cj := filepath.Join(dir, "client.json")
	info, err := os.Stat(cj)
	if err != nil {
		t.Fatalf("client.json missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("client.json mode = %v, want 0600", info.Mode().Perm())
	}
	var client struct {
		Port  int    `json:"port"`
		Token string `json:"token"`
	}
	b, _ := os.ReadFile(cj)
	_ = json.Unmarshal(b, &client)
	if client.Token != c.APIToken || client.Port != c.ListenPort {
		t.Fatalf("client.json mismatch: %+v vs cfg %d/%s", client, c.ListenPort, c.APIToken)
	}
}

func TestLoadReusesExistingToken(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	_ = os.WriteFile(cfgPath, []byte(""), 0o600)
	c1, err := Load(cfgPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Load(cfgPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if c1.APIToken != c2.APIToken {
		t.Fatalf("token not stable across loads: %s vs %s", c1.APIToken, c2.APIToken)
	}
}
