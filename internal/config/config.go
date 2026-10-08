// Package config is a minimal, self-contained configuration model that
// carries only what the mtproto package needs. It intentionally mirrors the
// field names/types of the corresponding types in the upstream b4 project
// (github.com/DanielLavrushin/b4/src/config) so the mtproto package could be
// copied over unmodified.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// SelfDialMark is the SO_MARK b4 places on connections it opens itself so
// its own netfilter rules do not re-intercept them. A standalone proxy has
// no such rules, so this is 0 by default; it is kept configurable in case
// you run this next to something that does mark-based routing.
var SelfDialMark uint32 = 0

// SelfDialNoDPIBit is the bit b4 ORs into SelfDialMark on relay connections
// so its DPI engine leaves them alone. There is no DPI engine here, so it is
// 0 and relay connections carry SelfDialMark unchanged.
const SelfDialNoDPIBit = uint32(0)

// MTProtoSecret is one configured (or auto-generated) fake-TLS secret.
type MTProtoSecret struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Secret  string `json:"secret"`
	Enabled bool   `json:"enabled"`
	// MaxNetworks caps how many distinct client networks (one IPv4 address, or
	// one /64 for IPv6) may use this secret at once; 0 means unlimited. A
	// connection from a network beyond the limit is refused rather than closing
	// an existing one, so handing a secret to one more person than intended
	// costs that person a connection rather than someone already using it.
	MaxNetworks int `json:"max_networks,omitempty"`
}

func (m *MTProtoConfig) EffectiveSecrets() []MTProtoSecret {
	out := make([]MTProtoSecret, 0, len(m.Secrets))
	for _, s := range m.Secrets {
		if s.Enabled && strings.TrimSpace(s.Secret) != "" {
			out = append(out, s)
		}
	}
	return out
}

func (m *MTProtoConfig) FirstEnabledSecret() string {
	for _, s := range m.Secrets {
		if s.Enabled && strings.TrimSpace(s.Secret) != "" {
			return s.Secret
		}
	}
	return ""
}

// MTProtoWebProxyConfig controls the optional Telegram Desktop "WEB proxy"
// relay: an HTTPS listener of its own (Port) that answers Hostname as the
// relay and every other host with a placeholder page.
type MTProtoWebProxyConfig struct {
	Enabled  bool   `json:"enabled"`
	Hostname string `json:"hostname"`
	// Port is the relay listener's port on BindAddress; 0 leaves the relay
	// off, since b4lite has no web server of its own to serve it from.
	Port int `json:"port"`
	// TLSCert/TLSKey are the PEM certificate and key for the relay port.
	// Without them it serves plain HTTP, which needs a TLS-terminating
	// proxy in front, as Telegram Desktop only speaks HTTPS.
	TLSCert string `json:"tls_cert"`
	TLSKey  string `json:"tls_key"`
}

// MTProtoConfig is the full set of knobs the mtproto server understands.
type MTProtoConfig struct {
	Enabled           bool            `json:"enabled"`
	Port              int             `json:"port"`
	BindAddress       string          `json:"bind_address"`
	MaxConnections    int             `json:"max_connections"` // 0 = default (2048)
	TCPUserTimeoutSec int             `json:"tcp_user_timeout_sec"`
	IdleTimeoutSec    int             `json:"idle_timeout_sec"`
	BridgeWaitSec     int             `json:"bridge_wait_sec"`
	Secrets           []MTProtoSecret `json:"secrets,omitempty"`
	FakeSNI           string          `json:"fake_sni"`
	DCRelay           string          `json:"dc_relay"`
	UpstreamMode      string          `json:"upstream_mode"` // "auto" | "ws" | "tcp"
	WSCustomDomain    string          `json:"ws_custom_domain"`
	WSEndpointHost    string          `json:"ws_endpoint_host"`
	// WSFrontSNI is an alternative TLS name (e.g. "sprinthost.ru") tried
	// for Telegram's WS edge after a handshake for kws*.web.telegram.org
	// fails, for networks that block those names. Empty or "off" disables
	// it; a handshake that does not end on a telegram.org certificate is
	// closed.
	WSFrontSNI     string `json:"ws_front_sni"`
	CFProxyEnabled bool   `json:"cfproxy_enabled"`
	CFProxyURL     string `json:"cfproxy_url"`
	CFWorkerDomain string `json:"cfworker_domain"`

	DCFallbackEnabled bool   `json:"dc_fallback_enabled"`
	DCFallbackURL     string `json:"dc_fallback_url"`

	WebProxy MTProtoWebProxyConfig `json:"web_proxy"`

	// BridgeSkipNativeEdge is only meaningful together with the transparent
	// WebSocket bridge (see internal/mtproto/transparent.go); left here so
	// the package compiles unmodified. Not exposed over JSON.
	BridgeSkipNativeEdge bool `json:"-"`

	// CFWorkerDPI lets b4's DPI-bypass sets process connections to the
	// Cloudflare Worker. There are no sets here; kept so the package
	// compiles unmodified. Not exposed over JSON.
	CFWorkerDPI bool `json:"-"`
}

// QueueConfig only carries the one field the mtproto package reads
// (whether to consider AAAA/IPv6 addresses when resolving Telegram DCs).
type QueueConfig struct {
	IPv4Enabled bool `json:"ipv4"`
	IPv6Enabled bool `json:"ipv6"`
	// Mark is only compared for equality by the mtproto package (to decide
	// whether a config change requires restarting dial pools); it plays no
	// routing role in this standalone build.
	Mark uint `json:"-"`
}

// Config is the top-level document. Only ConfigPath, Queue and
// System.MTProto are used by the mtproto package; Version is kept so the
// JSON file is forward-compatible if you ever grow this config.
type Config struct {
	Version    int    `json:"version"`
	ConfigPath string `json:"-"`

	Queue  QueueConfig  `json:"queue"`
	System SystemConfig `json:"system"`
}

// TelegramInUse reports whether anything needs Telegram's DC list and the
// Cloudflare proxy list refreshed. Upstream also counts the transparent
// bridge and routing sets; here only the proxy itself can use them.
func (c *Config) TelegramInUse() bool {
	return c != nil && c.System.MTProto.Enabled
}

type SystemConfig struct {
	MTProto MTProtoConfig `json:"mtproto"`

	// WebServer stands in for b4's web interface settings, whose TLS pair
	// the WEB proxy relay falls back to. There is no web interface here;
	// kept so the package compiles unmodified. Not exposed over JSON.
	WebServer WebServerConfig `json:"-"`
}

type WebServerConfig struct {
	TLSCert string
	TLSKey  string
}

const (
	TGDCFallbackURL  = "https://proxy.lavrush.in/telegram/getProxyConfig"
	TGCFProxyURL     = "https://raw.githubusercontent.com/Flowseal/tg-ws-proxy/main/.github/cfproxy-domains.txt"
	TGFakeSNI        = "storage.googleapis.com"
	TGWSEndpointHost = "149.154.167.220"
)

// DefaultConfig mirrors b4's out-of-the-box MTProto defaults.
var DefaultConfig = newDefaultConfig()

func newDefaultConfig() Config {
	return Config{
		Version: 1,
		Queue: QueueConfig{
			IPv6Enabled: true,
		},
		System: SystemConfig{
			MTProto: MTProtoConfig{
				Enabled:           true,
				Port:              3128,
				BindAddress:       "0.0.0.0",
				FakeSNI:           TGFakeSNI,
				UpstreamMode:      "auto",
				CFProxyEnabled:    true,
				CFProxyURL:        TGCFProxyURL,
				DCFallbackEnabled: true,
				DCFallbackURL:     TGDCFallbackURL,
			},
		},
	}
}

// LoadFromFile reads a JSON config from path into c. A missing file is not
// an error (the caller gets the zero/default value it started with).
func (c *Config) LoadFromFile(path string) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	return json.Unmarshal(data, c)
}

// SaveToFile writes c as indented JSON to path, creating parent directories
// as needed. Called by the mtproto package after it auto-generates a secret,
// so the same secret survives a restart.
func (c *Config) SaveToFile(path string) error {
	if path == "" {
		return nil
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
