package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/linfulongnet/gitlab-ci-mr-pipelines-hooks/internal/gitlab"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- Evaluate 判定逻辑测试 ---

func newPayload(action string, draft bool, from, to *bool) *Payload {
	p := &Payload{
		ObjectKind: "merge_request",
	}
	p.ObjectAttributes.Action = action
	p.ObjectAttributes.Draft = draft
	if from != nil && to != nil {
		p.Changes.Draft = &struct {
			From bool `json:"from"`
			To   bool `json:"to"`
		}{From: *from, To: *to}
	}
	return p
}

func boolPtr(b bool) *bool { return &b }

func TestEvaluateDraftToReady(t *testing.T) {
	p := newPayload("update", false, boolPtr(true), boolPtr(false))
	r := p.Evaluate()
	if !r.Triggered {
		t.Fatalf("期望触发，实际: %+v", r)
	}
}

func TestEvaluateNotMergeRequest(t *testing.T) {
	p := newPayload("update", false, boolPtr(true), boolPtr(false))
	p.ObjectKind = "push"
	if r := p.Evaluate(); r.Triggered {
		t.Fatalf("非 MR 事件不应触发: %+v", r)
	}
}

func TestEvaluateNotUpdateAction(t *testing.T) {
	// 打开新 MR 时 action=open，不应触发
	p := newPayload("open", true, nil, nil)
	if r := p.Evaluate(); r.Triggered {
		t.Fatalf("open 事件不应触发: %+v", r)
	}
}

func TestEvaluateReadyToDraftIgnored(t *testing.T) {
	p := newPayload("update", true, boolPtr(false), boolPtr(true))
	if r := p.Evaluate(); r.Triggered {
		t.Fatalf("ready -> draft 不应触发: %+v", r)
	}
}

func TestEvaluateDraftUnchangedIgnored(t *testing.T) {
	p := newPayload("update", false, boolPtr(false), boolPtr(false))
	if r := p.Evaluate(); r.Triggered {
		t.Fatalf("draft 状态未变化不应触发: %+v", r)
	}
}

func TestEvaluateMissingChangesIgnored(t *testing.T) {
	// GitLab 17.10+ 一定携带 changes.draft；缺失视为异常并忽略
	p := newPayload("update", false, nil, nil)
	if r := p.Evaluate(); r.Triggered {
		t.Fatalf("缺失 changes.draft 不应触发: %+v", r)
	}
}

// --- HTTP Handler 测试 ---

type fakeTriggerer struct {
	calledWith struct{ projectID, iid int }
	pipeline   *gitlab.Pipeline
	err        error
	calls      int
}

func (f *fakeTriggerer) TriggerMRPipeline(_ context.Context, projectID, iid int) (*gitlab.Pipeline, error) {
	f.calls++
	f.calledWith.projectID = projectID
	f.calledWith.iid = iid
	return f.pipeline, f.err
}

func fullPayloadJSON() []byte {
	payload := map[string]any{
		"object_kind": "merge_request",
		"object_attributes": map[string]any{
			"action":            "update",
			"draft":             false,
			"iid":               42,
			"title":             "feat: add hooks",
			"source_branch":     "feature/hooks",
			"target_project_id": 7,
		},
		"project": map[string]any{"id": 7},
		"changes": map[string]any{
			"draft": map[string]any{"from": true, "to": false},
		},
	}
	b, _ := json.Marshal(payload)
	return b
}

func TestHandlerTriggersPipeline(t *testing.T) {
	fake := &fakeTriggerer{
		pipeline: &gitlab.Pipeline{ID: 123, Status: "pending", WebURL: "https://gitlab.example.com/pipe/123"},
	}
	h := NewHandler(fake, "secret", testLogger())

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(fullPayloadJSON()))
	req.Header.Set("X-Gitlab-Token", "secret")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("期望 201，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if fake.calls != 1 {
		t.Fatalf("期望触发 1 次流水线，实际 %d", fake.calls)
	}
	if fake.calledWith.projectID != 7 || fake.calledWith.iid != 42 {
		t.Fatalf("期望调用 (7, 42)，实际 (%d, %d)", fake.calledWith.projectID, fake.calledWith.iid)
	}

	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != "triggered" {
		t.Fatalf("响应异常: %v", resp)
	}
}

func TestHandlerRejectsBadSecret(t *testing.T) {
	h := NewHandler(&fakeTriggerer{}, "secret", testLogger())

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(fullPayloadJSON()))
	req.Header.Set("X-Gitlab-Token", "wrong")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("期望 403，实际 %d", rec.Code)
	}
}

func TestHandlerIgnoresNonReadyEvent(t *testing.T) {
	fake := &fakeTriggerer{pipeline: &gitlab.Pipeline{ID: 1}}
	h := NewHandler(fake, "", testLogger())

	// ready -> draft，不应触发
	payload := fullPayloadJSON()
	var m map[string]any
	_ = json.Unmarshal(payload, &m)
	attrs := m["object_attributes"].(map[string]any)
	attrs["draft"] = true
	changes := m["changes"].(map[string]any)
	changes["draft"] = map[string]any{"from": false, "to": true}
	b, _ := json.Marshal(m)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望 200 ignored，实际 %d", rec.Code)
	}
	if fake.calls != 0 {
		t.Fatalf("不应触发流水线，实际触发 %d 次", fake.calls)
	}
}
