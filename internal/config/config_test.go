package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultsAndRelativeDataPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"base_url":"http://radio.local:8080","rules":[{"station":"LFR","match":"番組"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != filepath.Join(dir, "data") || c.LookbackDays != 2 || c.PollInterval != "30m" {
		t.Fatal(c)
	}
}

func TestRejectsConfigurationMistakes(t *testing.T) {
	for _, fragment := range []string{
		`"poll_interval":"0s"`, `"poll_interval":"bad"`, `"lookback_days":0`, `"lookback_days":8`,
		`"record_timeout":"0s"`, `"record_timeout":"25h"`, `"listen":"127.0.0.1"`, `"listen":":99999"`,
		`"data_dir":""`, `"title":" "`, `"poll_intervel":"1m"`,
		`"rules":[{"station":"../bad","match":"番組"}]`, `"rules":[{"station":"LFR","match":" "}]`,
	} {
		t.Run(fragment, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			_ = os.WriteFile(path, []byte(`{"base_url":"http://radio.local",`+fragment+`}`), 0600)
			if _, err := Load(path); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "config.json")
	_ = os.WriteFile(path, []byte(`{"base_url":"http://radio.local"} {}`), 0600)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "one JSON") {
		t.Fatal(err)
	}
}
