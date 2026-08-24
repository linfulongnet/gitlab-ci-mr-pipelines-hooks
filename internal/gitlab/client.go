// Package gitlab 封装对 GitLab REST API 的调用。
package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// New 创建一个 GitLab API 客户端。
// baseURL 应为 GitLab 根地址（不含 /api/v4），例如 "http://gitlab.example.com:8929"。
func New(baseURL, token string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
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
