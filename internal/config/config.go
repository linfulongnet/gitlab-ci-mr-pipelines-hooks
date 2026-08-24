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

// GitLabConfig 私有化 GitLab 连接配置。
type GitLabConfig struct {
	// BaseURL GitLab 根地址（含端口），例如 "http://gitlab.example.com:8929"
	BaseURL string `yaml:"base_url"`
	// Token 用于调用 GitLab API 的访问令牌（需要 api 权限）
	Token string `yaml:"token"`
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
			Addr: ":8080",
		},
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
	if c.GitLab.BaseURL == "" {
		return fmt.Errorf("缺少必需配置 gitlab.base_url")
	}
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
