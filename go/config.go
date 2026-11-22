package main

import (
	"net/url"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type config struct {
	UpstreamBaseURL string `yaml:"upstream_base_url"`
	AdminSecret     string `yaml:"admin_secret"`
	APIKey          string `yaml:"api_key"`
	TimeoutSec      int    `yaml:"timeout_sec"`
	RegisterOnAuth  bool   `yaml:"register_on_auth"`
	BridgeMode      string `yaml:"bridge_mode"`
	SessionDir      string `yaml:"session_dir"`
	OAuthPort       int    `yaml:"oauth_port"`
	OAuthTimeoutSec int    `yaml:"oauth_timeout_sec"`
	DefaultIssuer   string `yaml:"default_issuer"`
	DefaultTenant   string `yaml:"default_tenant"`
	DefaultRegion   string `yaml:"default_region"`
}

var (
	configMu      sync.RWMutex
	currentConfig = defaultConfig()
)

func defaultConfig() config {
	return config{
		UpstreamBaseURL: "http://127.0.0.1:19100",
		TimeoutSec:      120,
		RegisterOnAuth:  false,
		BridgeMode:      "direct",
		DefaultIssuer:   "https://identitycenter.amazonaws.com/ssoins-7223d6ff6fff3554",
		DefaultTenant:   "https://qbs-cellb8886375-16f2-46de-84f2-ae1c2b595473.dp.appintegrations.us-east-1.prod.plato.ai.aws.dev",
		DefaultRegion:   "us-east-1",
	}
}

func getConfig() config {
	configMu.RLock()
	defer configMu.RUnlock()
	return currentConfig
}

func configure(raw []byte) error {
	c := defaultConfig()
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &c); err != nil {
			return err
		}
	}
	c.UpstreamBaseURL = strings.TrimRight(strings.TrimSpace(c.UpstreamBaseURL), "/")
	if c.UpstreamBaseURL == "" {
		c.UpstreamBaseURL = "http://127.0.0.1:19100"
	}
	u, err := url.Parse(c.UpstreamBaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errInvalidConfig("upstream_base_url must be an absolute HTTP(S) URL")
	}
	if c.TimeoutSec < 5 || c.TimeoutSec > 600 {
		c.TimeoutSec = 120
	}
	if strings.TrimSpace(c.BridgeMode) == "" {
		c.BridgeMode = "upstream"
	}
	if c.BridgeMode != "upstream" && c.BridgeMode != "direct" {
		return errInvalidConfig("bridge_mode must be upstream or direct")
	}
	if c.OAuthTimeoutSec < 30 || c.OAuthTimeoutSec > 1800 {
		c.OAuthTimeoutSec = 600
	}
	configMu.Lock()
	currentConfig = c
	configMu.Unlock()
	return nil
}

type configError struct{ msg string }

func (e *configError) Error() string    { return e.msg }
func errInvalidConfig(msg string) error { return &configError{msg: msg} }

func timeout() time.Duration { return time.Duration(getConfig().TimeoutSec) * time.Second }
