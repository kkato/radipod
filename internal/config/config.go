package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kkato/radipod/internal/feed"
	"github.com/kkato/radipod/internal/radiko"
)

type Rule struct {
	Station string `json:"station"`
	Match   string `json:"match"`
}

type Config struct {
	DataDir       string `json:"data_dir"`
	Listen        string `json:"listen"`
	BaseURL       string `json:"base_url"`
	Title         string `json:"title"`
	PollInterval  string `json:"poll_interval"`
	LookbackDays  int    `json:"lookback_days"`
	RecordTimeout string `json:"record_timeout"`
	Rules         []Rule `json:"rules"`
}

func Load(path string) (Config, error) {
	c := Config{DataDir: "data", Listen: "127.0.0.1:8080", Title: "radipod", PollInterval: "30m", LookbackDays: 2, RecordTimeout: "30m"}
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, fmt.Errorf("config: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return c, fmt.Errorf("config must contain one JSON object")
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	if !filepath.IsAbs(c.DataDir) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return c, err
		}
		c.DataDir = filepath.Join(filepath.Dir(abs), c.DataDir)
	}
	return c, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.DataDir) == "" || strings.TrimSpace(c.Title) == "" {
		return fmt.Errorf("data_dir and title must not be empty")
	}
	if _, err := feed.ValidateBaseURL(c.BaseURL); err != nil {
		return err
	}
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen must be host:port: %w", err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("invalid listen port")
	}
	interval, err := time.ParseDuration(c.PollInterval)
	if err != nil || interval < time.Minute || interval > 24*time.Hour {
		return fmt.Errorf("poll_interval must be between 1m and 24h")
	}
	timeout, err := time.ParseDuration(c.RecordTimeout)
	if err != nil || timeout <= 0 || timeout > 24*time.Hour {
		return fmt.Errorf("record_timeout must be positive and at most 24h")
	}
	if c.LookbackDays < 1 || c.LookbackDays > 7 {
		return fmt.Errorf("lookback_days must be between 1 and 7 (including current broadcast day)")
	}
	for i, r := range c.Rules {
		if err := radiko.ValidateStation(r.Station); err != nil {
			return fmt.Errorf("rule %d: %w", i+1, err)
		}
		if strings.TrimSpace(r.Match) == "" {
			return fmt.Errorf("rule %d: match must not be blank", i+1)
		}
	}
	return nil
}
