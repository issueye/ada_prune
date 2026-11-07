// Package command_code adapts the Command Code subscription upstream.
//
// Everything vendor-specific about that upstream lives here: the request
// envelope, the CLI identity headers, the device fingerprint and its reporting
// cadence, session affinity, the NDJSON event stream, quota vocabulary, the
// model catalogue and the account usage calls.
//
// The rest of the gateway only sees gateway.Adapter and the canonical IR.
package command_code

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/learningdog1/ada_prune/internal/gateway"
)

// Defaults of the adapter configuration. They are the values a newly created
// provider starts with.
const (
	// DefaultAPIBase is the public upstream root.
	DefaultAPIBase = "https://api.commandcode.ai"
	// ProtocolVersion is the CLI wire version this adapter implements. Claiming a
	// version whose shape we do not speak is itself a suspicious signal, so this
	// constant moves only together with the code that matches it.
	ProtocolVersion = "1.53.1"
	// DefaultProjectDir is the fake working directory used when none is set.
	DefaultProjectDir = `C:\Users\dev\projects\app`
	// DefaultSessionTTLHours is how long one credential's session id lives.
	DefaultSessionTTLHours = 12
	// DefaultReportRefreshHours is how often the device identity is re-announced.
	DefaultReportRefreshHours = 8
)

// Catalog modes.
const (
	// CatalogBuiltin only uses the embedded model list.
	CatalogBuiltin = "builtin"
	// CatalogDynamic only uses the upstream list.
	CatalogDynamic = "dynamic"
	// CatalogHybrid tries the upstream list and falls back to the builtin one.
	CatalogHybrid = "hybrid"
)

// Config is the adapter_config payload.
type Config struct {
	// APIBase overrides the provider BaseURL when the latter is empty.
	APIBase string `json:"apiBase"`
	// ProtocolVersion is the x-command-code-version value.
	ProtocolVersion string `json:"protocolVersion"`
	// CLIMode is the envelope's "mode" field.
	CLIMode string `json:"cliMode"`
	// CLISessionMode is the lifecycle event's metadata.mode.
	CLISessionMode string `json:"cliSessionMode"`
	// ProjectDir is the fake working directory: it decides the project slug and
	// the fingerprint's platform profile.
	ProjectDir string `json:"projectDir"`
	// FingerprintSalt selects which fake device a credential derives. Empty uses
	// the adapter default; the component hashing salt itself is fixed upstream.
	FingerprintSalt string `json:"fingerprintSalt"`
	// EmptySystemPlaceholder puts a single space in params.system when the client
	// sent no system prompt. Without it the upstream injects ~7.5K tokens of its
	// own default prompt.
	EmptySystemPlaceholder bool `json:"emptySystemPlaceholder"`
	// ZDR adds x-cmd-zdr: 1 to generation and init calls.
	ZDR bool `json:"zdr"`
	// SessionTTLHours is how long a credential's session id is reused.
	SessionTTLHours int `json:"sessionTtlHours"`
	// ReportRefreshHours is the identity re-announce interval.
	ReportRefreshHours int `json:"reportRefreshHours"`
	// Catalog selects how the model list is sourced.
	Catalog string `json:"modelCatalog"`
}

// parseConfig applies defaults and decodes the provider's adapter_config.
func parseConfig(cfg gateway.ProviderConfig) (Config, error) {
	c := Config{
		APIBase:                DefaultAPIBase,
		ProtocolVersion:        ProtocolVersion,
		CLIMode:                "agent",
		CLISessionMode:         "interactive",
		ProjectDir:             DefaultProjectDir,
		EmptySystemPlaceholder: true,
		SessionTTLHours:        DefaultSessionTTLHours,
		ReportRefreshHours:     DefaultReportRefreshHours,
		Catalog:                CatalogHybrid,
	}

	if err := decodeInto(cfg.AdapterConfig, &c); err != nil {
		return c, err
	}
	if strings.TrimSpace(cfg.BaseURL) != "" {
		c.APIBase = strings.TrimSpace(cfg.BaseURL)
	}
	if c.ProtocolVersion == "" {
		c.ProtocolVersion = ProtocolVersion
	}
	if c.CLIMode == "" {
		c.CLIMode = "agent"
	}
	if c.ProjectDir == "" {
		c.ProjectDir = DefaultProjectDir
	}
	if c.SessionTTLHours <= 0 {
		c.SessionTTLHours = DefaultSessionTTLHours
	}
	if c.ReportRefreshHours <= 0 {
		c.ReportRefreshHours = DefaultReportRefreshHours
	}
	if c.Catalog == "" {
		c.Catalog = CatalogHybrid
	}
	return c, nil
}

// validateConfig checks the adapter_config payload.
func validateConfig(cfg gateway.ProviderConfig) error {
	c, err := parseConfig(cfg)
	if err != nil {
		return err
	}
	if err := validateBaseURL(c.APIBase); err != nil {
		return err
	}
	switch c.CLIMode {
	case "agent", "chat":
	default:
		return fmt.Errorf("cliMode must be \"agent\" or \"chat\", got %q", c.CLIMode)
	}
	switch c.Catalog {
	case CatalogBuiltin, CatalogDynamic, CatalogHybrid:
	default:
		return fmt.Errorf("modelCatalog must be builtin, dynamic or hybrid, got %q", c.Catalog)
	}
	if c.SessionTTLHours > 24*30 {
		return fmt.Errorf("sessionTtlHours is unreasonably large (%d)", c.SessionTTLHours)
	}
	return nil
}

func validateBaseURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("apiBase or the provider base URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("apiBase is not a valid URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("apiBase must use http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("apiBase has no host")
	}
	return nil
}

// configSchema describes the fields for the control plane's dynamic form.
func configSchema() []gateway.ConfigField {
	return []gateway.ConfigField{
		{
			Key:         "apiBase",
			Label:       "API 根地址",
			Type:        "string",
			Default:     DefaultAPIBase,
			Required:    false,
			Description: "留空则使用供应商 BaseURL",
		},
		{
			Key:         "cliMode",
			Label:       "CLI 模式",
			Type:        "select",
			Default:     "agent",
			Options:     []string{"agent", "chat"},
			Description: "写入信封的 mode 字段",
		},
		{
			Key:         "cliSessionMode",
			Label:       "会话模式",
			Type:        "select",
			Default:     "interactive",
			Options:     []string{"interactive", "batch"},
			Description: "生命周期上报的 metadata.mode",
		},
		{
			Key:         "projectDir",
			Label:       "伪工作目录",
			Type:        "string",
			Default:     DefaultProjectDir,
			Description: "决定项目 slug 与指纹的平台档案，改动会改变设备身份",
		},
		{
			Key:         "fingerprintSalt",
			Label:       "指纹盐",
			Type:        "string",
			Default:     "",
			Secret:      true,
			Description: "留空使用默认派生；同一条密钥永远得到同一台设备",
		},
		{
			Key:         "emptySystemPlaceholder",
			Label:       "空 system 占位",
			Type:        "bool",
			Default:     true,
			Description: "客户端没给 system 时塞一个空格，避免上游注入约 7.5K token 默认提示",
		},
		{
			Key:         "zdr",
			Label:       "零数据保留",
			Type:        "bool",
			Default:     false,
			Description: "在生成与初始化请求上带 x-cmd-zdr: 1",
		},
		{
			Key:         "sessionTtlHours",
			Label:       "会话 TTL（小时）",
			Type:        "int",
			Default:     DefaultSessionTTLHours,
			Description: "同一条密钥复用会话 id 的时长",
		},
		{
			Key:         "reportRefreshHours",
			Label:       "身份上报间隔（小时）",
			Type:        "int",
			Default:     DefaultReportRefreshHours,
			Description: "指纹与生命周期事件的重报周期",
		},
		{
			Key:         "modelCatalog",
			Label:       "模型目录",
			Type:        "select",
			Default:     CatalogHybrid,
			Options:     []string{CatalogBuiltin, CatalogDynamic, CatalogHybrid},
			Description: "内置 / 上游动态 / 先动态后回退内置",
		},
	}
}
