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
	"strconv"
	"sync"
	"time"

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

// mrState 记录某个 MR 的已知草稿状态。
type mrState struct {
	draft    bool
	lastSeen time.Time
}

// stateTracker 跟踪每个 MR 的草稿状态，用于检测 draft -> ready 转换。
//
// 说明：GitLab webhook 的 changes.draft 字段并不可靠（实测在"Mark as ready"
// 时可能缺失或报告 from:false,to:false），因此改为跟踪 object_attributes.draft
// 这一可靠字段，通过状态机推导状态转换。
type stateTracker struct {
	mu   sync.Mutex
	seen map[string]*mrState
}

func newStateTracker() *stateTracker {
	return &stateTracker{seen: make(map[string]*mrState)}
}

func mrKey(projectID, iid int) string {
	return strconv.Itoa(projectID) + ":" + strconv.Itoa(iid)
}

// Evaluate 判定该事件是否为"Mark as ready"转换，并更新内部状态。
//
// 判定规则（状态机）：
//   - 首次见到某 MR（无记录）：记录当前 draft，不触发（冷启动，避免误触发）；
//   - action 为 open/reopen：记录当前 draft，不触发（新建/重开不算转换）；
//   - action 为 update 且上次为 true、当前为 false：触发；
//   - action 为 close/merge：删除记录，不触发；
//   - 其它情况：仅更新记录，不触发。
//
// 返回的 ReadyTransition 仅表示"是否应触发"，不负责推进状态——
// 状态推进由 Advance 在触发成功后调用，以保证 GitLab 重试时能再次尝试。
func (t *stateTracker) Evaluate(p *Payload) ReadyTransition {
	if p.ObjectKind != "merge_request" {
		return ReadyTransition{false, "非 merge_request 事件，忽略"}
	}

	attrs := &p.ObjectAttributes
	key := mrKey(attrs.TargetProjectID, attrs.IID)

	t.mu.Lock()
	defer t.mu.Unlock()

	prev, exists := t.seen[key]

	switch attrs.Action {
	case "close", "merge":
		delete(t.seen, key)
		return ReadyTransition{false, "action 为 " + attrs.Action + "，清除状态，忽略"}

	case "open", "reopen":
		t.seen[key] = &mrState{draft: attrs.Draft, lastSeen: time.Now()}
		return ReadyTransition{false, "action 为 " + attrs.Action + "，记录 draft=" + boolStr(attrs.Draft) + "，忽略"}

	case "update":
		if !exists {
			// 冷启动：首次见到该 MR，仅记录，不触发
			t.seen[key] = &mrState{draft: attrs.Draft, lastSeen: time.Now()}
			return ReadyTransition{false, "首次见到该 MR（冷启动），记录 draft=" + boolStr(attrs.Draft) + "，不触发"}
		}
		if prev.draft && !attrs.Draft {
			// 检测到 draft: true -> false，触发；状态推进由 Advance 完成
			return ReadyTransition{true, "检测到 draft: true -> false（Mark as ready）"}
		}
		prev.draft = attrs.Draft
		prev.lastSeen = time.Now()
		return ReadyTransition{false, "draft 状态未发生 true->false 转换（prev=" + boolStr(prev.draft) + "），忽略"}

	default:
		// 其它 action（approved 等）：仅在没有记录时记录，不触发
		if !exists {
			t.seen[key] = &mrState{draft: attrs.Draft, lastSeen: time.Now()}
		}
		return ReadyTransition{false, "action 为 " + attrs.Action + "，忽略"}
	}
}

// Advance 在触发成功后推进状态：将 MR 的草稿状态更新为当前值。
// 仅在触发成功时调用，保证失败时保留旧状态以便 GitLab 重试再次触发。
func (t *stateTracker) Advance(p *Payload) {
	attrs := &p.ObjectAttributes
	key := mrKey(attrs.TargetProjectID, attrs.IID)

	t.mu.Lock()
	defer t.mu.Unlock()

	if s, ok := t.seen[key]; ok {
		s.draft = attrs.Draft
		s.lastSeen = time.Now()
	}
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

// NewHandler 创建 Webhook 处理器。
func NewHandler(gl Triggerer, webhookSecret string, logger *slog.Logger) *Handler {
	return &Handler{
		gitlab:        gl,
		webhookSecret: webhookSecret,
		logger:        logger,
		state:         newStateTracker(),
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
