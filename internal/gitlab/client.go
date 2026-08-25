// Package gitlab 封装对 GitLab REST API 的调用。
package gitlab

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// Pipeline 是 GitLab API 返回的流水线对象摘要。
type Pipeline struct {
	ID        int    `json:"id"`
	IID       int    `json:"iid"`
	ProjectID int    `json:"project_id"`
	Status    string `json:"status"`
	Ref       string `json:"ref"`
	SHA       string `json:"sha"`
	WebURL    string `json:"web_url"`
}

// Client 是 GitLab API 客户端。
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// Options 是 GitLab 客户端的可选配置。
type Options struct {
	// Timeout 请求超时时间
	Timeout time.Duration
	// InsecureSkipVerify 跳过 TLS 证书校验（自签名/无 IP SAN 证书场景）
	InsecureSkipVerify bool
	// CACertFile 自定义 CA 证书文件路径（PEM 格式）
	CACertFile string
}

// New 创建一个 GitLab API 客户端。
// baseURL 应为 GitLab 根地址（不含 /api/v4），例如 "http://gitlab.example.com:8929"。
func New(baseURL, token string, opts Options) (*Client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}

	// 优先使用自定义 CA 证书；否则按需跳过校验
	if opts.CACertFile != "" {
		pem, err := os.ReadFile(opts.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("读取 CA 证书失败: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA 证书文件 %s 中没有有效的 PEM 证书", opts.CACertFile)
		}
		tlsConfig.RootCAs = pool
	} else if opts.InsecureSkipVerify {
		tlsConfig.InsecureSkipVerify = true
	}

	transport := &http.Transport{TLSClientConfig: tlsConfig}

	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout:   opts.Timeout,
			Transport: transport,
		},
	}, nil
}

// TriggerMRPipeline 为指定 MR 触发一次流水线：
//
//	POST /api/v4/projects/:id/merge_requests/:iid/pipelines
//
// 成功时返回 GitLab 创建出的流水线对象（HTTP 201）。
func (c *Client) TriggerMRPipeline(ctx context.Context, projectID, iid int) (*Pipeline, error) {
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/merge_requests/%s/pipelines",
		c.baseURL,
		url.PathEscape(strconv.Itoa(projectID)),
		url.PathEscape(strconv.Itoa(iid)),
	)

	// 请求体留空对象即可，GitLab 默认对 MR 的源分支创建流水线。
	body := bytes.NewBufferString(`{}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 GitLab 失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitLab 返回 %s: %s", resp.Status, truncate(string(respBody), 500))
	}

	var pipeline Pipeline
	if err := json.Unmarshal(respBody, &pipeline); err != nil {
		return nil, fmt.Errorf("解析 GitLab 响应失败: %w", err)
	}
	return &pipeline, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
