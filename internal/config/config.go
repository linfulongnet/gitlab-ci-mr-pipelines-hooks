// Package config 负责从环境变量加载并校验网关配置。
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// Config 保存网关运行所需的全部配置。
type Config struct {
	// ListenAddr HTTP 监听地址，例如 ":8080"
	ListenAddr string
	// GitLabBaseURL 私有化 GitLab 的根地址，例如 "http://gitlab.example.com:8929"
	GitLabBaseURL string
	// GitLabToken 用于调用 GitLab API 的访问令牌（需要 api 权限）
	GitLabToken string
	// WebhookSecret 在 GitLab Webhook 中配置的 Secret token，用于校验请求来源。
	// 为空时跳过校验（不推荐）。
	WebhookSecret string
	// TriggerOnUpdate 为 true 时，对任何 draft=false 的 MR 更新事件也触发流水线。
	// 用于 GitLab 旧版本不携带 changes.draft 的兼容场景，默认关闭。
	TriggerOnUpdate bool
	// PipelineTimeout 调用 GitLab API 的超时时间
	PipelineTimeout time.Duration
	// MaxBodyBytes Webhook 请求体的最大字节数
	MaxBodyBytes int64
}

// FromEnv 从环境变量读取配置并校验。
//
// 必需变量：
//   - GITLAB_BASE_URL
//   - GITLAB_TOKEN
//
// 可选变量：
//   - LISTEN_ADDR            默认 ":8080"
//   - GITLAB_WEBHOOK_SECRET  默认空（不校验）
//   - TRIGGER_ON_UPDATE      默认 false
//   - PIPELINE_TIMEOUT       默认 30s
//   - MAX_BODY_BYTES         默认 10MB
func FromEnv() (*Config, error) {
	cfg := &Config{
		ListenAddr:      envOr("LISTEN_ADDR", ":8080"),
		GitLabBaseURL:   os.Getenv("GITLAB_BASE_URL"),
		GitLabToken:     os.Getenv("GITLAB_TOKEN"),
		WebhookSecret:   os.Getenv("GITLAB_WEBHOOK_SECRET"),
		TriggerOnUpdate: envBool("TRIGGER_ON_UPDATE", false),
		PipelineTimeout: envDuration("PIPELINE_TIMEOUT", 30*time.Second),
		MaxBodyBytes:    envInt64("MAX_BODY_BYTES", 10<<20),
	}

	if cfg.GitLabBaseURL == "" {
		return nil, fmt.Errorf("缺少必需环境变量 GITLAB_BASE_URL")
	}
	if cfg.GitLabToken == "" {
		return nil, fmt.Errorf("缺少必需环境变量 GITLAB_TOKEN")
	}
	if cfg.PipelineTimeout <= 0 {
		return nil, fmt.Errorf("PIPELINE_TIMEOUT 必须为正数")
	}
	if cfg.MaxBodyBytes <= 0 {
		return nil, fmt.Errorf("MAX_BODY_BYTES 必须为正数")
	}

	// 规范化 base URL：补全协议、去掉尾部斜杠
	if !strings.Contains(cfg.GitLabBaseURL, "://") {
		cfg.GitLabBaseURL = "http://" + cfg.GitLabBaseURL
	}
	u, err := url.Parse(cfg.GitLabBaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("GITLAB_BASE_URL 不是合法的 http(s) 地址: %q", cfg.GitLabBaseURL)
	}
	cfg.GitLabBaseURL = strings.TrimRight(u.String(), "/")

	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return strings.EqualFold(v, "true") || v == "1"
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		var n int64
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}
