// Package config loads daemon configuration from a TOML file overlaid by
// environment variables, and bootstraps the loopback API token shared with
// the GNOME extension via client.json.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	BMURL      string `toml:"bm_url"`
	BMToken    string `toml:"bm_bearer_token"`
	BMUsername string `toml:"bm_username"`
	BMProject  string `toml:"bm_project"`
	ListenPort int    `toml:"listen_port"`
	APIToken   string `toml:"api_token"`

	GitHubIntervalSec int `toml:"github_interval_sec"`
	BMIntervalSec     int `toml:"bm_interval_sec"`
	MorningSweepHour  int `toml:"morning_sweep_hour"`

	StatsDir string `toml:"stats_dir"`
}

// Load reads cfgPath (TOML), applies env + defaults, ensures an API token
// exists, and writes client.json into stateDir for the extension.
func Load(cfgPath, stateDir string) (Config, error) {
	var c Config
	if b, err := os.ReadFile(cfgPath); err == nil {
		if err := toml.Unmarshal(b, &c); err != nil {
			return c, err
		}
	} else if !os.IsNotExist(err) {
		return c, err
	}

	if v := os.Getenv("BM_URL"); v != "" {
		c.BMURL = v
	}
	if v := os.Getenv("BM_BEARER_TOKEN"); v != "" {
		c.BMToken = v
	}
	if c.BMProject == "" {
		c.BMProject = "main"
	}
	if c.ListenPort == 0 {
		c.ListenPort = 47615
	}
	if c.GitHubIntervalSec == 0 {
		c.GitHubIntervalSec = 120
	}
	if c.BMIntervalSec == 0 {
		c.BMIntervalSec = 300
	}
	if c.MorningSweepHour == 0 {
		c.MorningSweepHour = 8
	}
	// Reuse a previously-bootstrapped token: if the TOML carries no api_token,
	// read the one written to client.json on a prior load so the token stays
	// stable across daemon restarts (the extension caches it).
	if c.APIToken == "" {
		if b, err := os.ReadFile(filepath.Join(stateDir, "client.json")); err == nil {
			var existing struct {
				Token string `json:"token"`
			}
			if json.Unmarshal(b, &existing) == nil {
				c.APIToken = existing.Token
			}
		}
	}
	if c.APIToken == "" {
		t, err := randomToken()
		if err != nil {
			return c, err
		}
		c.APIToken = t
	}

	if c.StatsDir == "" {
		base := os.Getenv("XDG_STATE_HOME")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, ".local", "state")
		}
		c.StatsDir = filepath.Join(base, "anti-tangent-mcp")
	}

	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return c, err
	}
	client := struct {
		Port  int    `json:"port"`
		Token string `json:"token"`
	}{c.ListenPort, c.APIToken}
	b, err := json.Marshal(client)
	if err != nil {
		return c, err
	}
	if err := os.WriteFile(filepath.Join(stateDir, "client.json"), b, 0o600); err != nil {
		return c, err
	}
	return c, nil
}

func randomToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
