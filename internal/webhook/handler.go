// Package webhook 实现 GitLab Merge Request Webhook 的接收与处理。
package webhook

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/linfulongnet/gitlab-ci-mr-pipelines-hooks/internal/gitlab"
)

// Payload 是 GitLab merge_request webhook 事件的请求体。
// 仅声明本项目关心的字段，其余字段忽略。
type Payload struct {
	ObjectKind string `json:"object_kind"`

	ObjectAttributes struct {
		Action          string `json:"action"`
		Draft           bool   `json:"draft"`
		IID             int    `json:"iid"`
		Title           string `json:"title"`
		SourceBranch    string `json:"source_branch"`
		TargetProjectID int    `json:"target_project_id"`
	} `json:"object_attributes"`

	Project struct {
		ID int `json:"id"`
	} `json:"project"`
}

// ReadyTransition 表示一次 draft -> ready 转换的判定结果。
type ReadyTransition struct {
	// Triggered 为 true 时表示应触发流水线
	Triggered bool
	// Reason 记录判定原因，用于日志
	Reason string
}

// Triggerer 触发 MR 流水线的接口，便于测试注入。
type Triggerer interface {
	TriggerMRPipeline(ctx context.Context, projectID, iid int) (*gitlab.Pipeline, error)
}

// Handler 处理 GitLab Webhook 请求。
type Handler struct {
	gitlab        Triggerer
	webhookSecret string
	logger        *slog.Logger
	state         *stateTracker
}

// NewHandler 创建 Webhook 处理器。stateFile 非空时启用状态持久化。
func NewHandler(gl Triggerer, webhookSecret, stateFile string, logger *slog.Logger) *Handler {
	return &Handler{
		gitlab:        gl,
		webhookSecret: webhookSecret,
		logger:        logger,
		state:         newStateTracker(stateFile, logger),
	}
}

// ServeHTTP 实现 http.Handler。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	body, err := readBody(r)
	if err != nil {
		h.logger.Warn("读取请求体失败", "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "invalid request body"})
		return
	}

	if !h.validSignature(r) {
		h.logger.Warn("Webhook 签名校验失败", "remote", r.RemoteAddr)
		writeJSON(w, http.StatusForbidden, map[string]string{"status": "error", "message": "invalid webhook secret"})
		return
	}

	var payload Payload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.logger.Warn("解析 Webhook 请求体失败", "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "invalid JSON"})
		return
	}

	attrs := payload.ObjectAttributes
	logger := h.logger.With(
		"mr_iid", attrs.IID,
		"action", attrs.Action,
		"draft", attrs.Draft,
		"source_branch", attrs.SourceBranch,
		"title", attrs.Title,
	)
	logger.Info("收到 Merge Request Webhook 事件")

	result := h.state.Evaluate(&payload)
	logger.Info("事件判定", "triggered", result.Triggered, "reason", result.Reason)

	if !result.Triggered {
		writeJSON(w, http.StatusOK, map[string]string{
			"status": "ignored",
			"reason": result.Reason,
		})
		return
	}

	projectID := attrs.TargetProjectID
	if projectID == 0 {
		projectID = payload.Project.ID
	}
	if projectID == 0 || attrs.IID == 0 {
		h.logger.Error("缺少 project id 或 mr iid，无法触发流水线",
			"project_id", projectID, "mr_iid", attrs.IID)
		writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "message": "missing project id or mr iid"})
		return
	}

	pipeline, err := h.gitlab.TriggerMRPipeline(r.Context(), projectID, attrs.IID)
	if err != nil {
		h.logger.Error("触发 MR 流水线失败", "project_id", projectID, "mr_iid", attrs.IID, "err", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"status": "error", "message": "failed to trigger pipeline"})
		return
	}

	// 触发成功后推进状态，保证 GitLab 重试时不会重复触发
	h.state.Advance(&payload)

	h.logger.Info("已触发 MR 流水线",
		"project_id", projectID,
		"mr_iid", attrs.IID,
		"pipeline_id", pipeline.ID,
		"status", pipeline.Status,
		"web_url", pipeline.WebURL,
	)
	writeJSON(w, http.StatusCreated, map[string]any{
		"status":      "triggered",
		"pipeline_id": pipeline.ID,
		"web_url":     pipeline.WebURL,
	})
}

// validSignature 校验 X-Gitlab-Token 请求头。
// 未配置 secret 时直接放行。
func (h *Handler) validSignature(r *http.Request) bool {
	if h.webhookSecret == "" {
		return true
	}
	got := r.Header.Get("X-Gitlab-Token")
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.webhookSecret)) == 1
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, errors.New("empty body")
	}
	const maxBody = 10 << 20 // 10MB
	return io.ReadAll(io.LimitReader(r.Body, maxBody))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
