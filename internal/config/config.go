// Package config 负责从配置文件加载并校验网关配置。
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 保存网关运行所需的全部配置。
type Config struct {
	// Listen HTTP 监听配置
	Listen ListenConfig `yaml:"listen"`
	// GitLab 私有化 GitLab 连接配置
	GitLab GitLabConfig `yaml:"gitlab"`
	// Webhook Webhook 校验配置
	Webhook WebhookConfig `yaml:"webhook"`
	// StateFile 状态持久化文件路径。网关重启后恢复 MR 草稿状态，
	// 避免冷启动误判。留空则不持久化（仅内存跟踪）。
	StateFile string `yaml:"state_file"`
	// PipelineTimeout 调用 GitLab API 的超时时间
	PipelineTimeout time.Duration `yaml:"pipeline_timeout"`
	// MaxBodyBytes Webhook 请求体的最大字节数
	MaxBodyBytes int64 `yaml:"max_body_bytes"`
}

// ListenConfig HTTP 监听配置。
type ListenConfig struct {
	// Addr 监听地址，例如 ":8080"
	Addr string `yaml:"addr"`
}

// GitLabConfig GitLab 连接配置。
type GitLabConfig struct {
	// BaseURL GitLab 根地址（含端口），例如 "http://gitlab.example.com:8929"。
	// 可选，默认使用公有 GitLab https://gitlab.com。
	BaseURL string `yaml:"base_url"`
	// Token 用于调用 GitLab API 的访问令牌（需要 api 权限）
	Token string `yaml:"token"`
	// InsecureSkipVerify 为 true 时跳过 TLS 证书校验。
	// 适用于私有化部署使用自签名证书或证书不含 IP SAN 的场景。
	// 注意：这会降低安全性，仅建议在内网使用。
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
	// CACertFile 自定义 CA 证书文件路径（PEM 格式）。
	// 当 GitLab 使用内部 CA 签发的证书时，指定该文件以信任它。
	// 与 InsecureSkipVerify 互斥，优先使用 CA 证书。
	CACertFile string `yaml:"ca_cert_file"`
}

// WebhookConfig Webhook 校验配置。
type WebhookConfig struct {
	// Secret 在 GitLab Webhook 中配置的 Secret token，用于校验请求来源。
	// 为空时跳过校验（不推荐）。
	Secret string `yaml:"secret"`
}

// Default 返回带默认值的配置。
func Default() *Config {
	return &Config{
		Listen: ListenConfig{
			Addr: ":9932",
		},
		GitLab: GitLabConfig{
			BaseURL: "https://gitlab.com",
		},
		StateFile:       "state.json",
		PipelineTimeout: 30 * time.Second,
		MaxBodyBytes:    10 << 20, // 10MB
	}
}

// Load 从指定路径读取 YAML 配置文件并校验。
// 未提供的字段使用默认值。
func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate 校验配置合法性并做规范化。
func (c *Config) Validate() error {
	if c.GitLab.Token == "" {
		return fmt.Errorf("缺少必需配置 gitlab.token")
	}
	if c.PipelineTimeout <= 0 {
		return fmt.Errorf("pipeline_timeout 必须为正数")
	}
	if c.MaxBodyBytes <= 0 {
		return fmt.Errorf("max_body_bytes 必须为正数")
	}

	// 规范化 base URL：补全协议、去掉尾部斜杠
	if !strings.Contains(c.GitLab.BaseURL, "://") {
		c.GitLab.BaseURL = "http://" + c.GitLab.BaseURL
	}
	u, err := url.Parse(c.GitLab.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("gitlab.base_url 不是合法的 http(s) 地址: %q", c.GitLab.BaseURL)
	}
	c.GitLab.BaseURL = strings.TrimRight(u.String(), "/")

	return nil
}
