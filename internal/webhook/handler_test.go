package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/linfulongnet/gitlab-ci-mr-pipelines-hooks/internal/gitlab"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- stateTracker 判定逻辑测试 ---

func newPayload(action string, draft bool, projectID, iid int) *Payload {
	p := &Payload{ObjectKind: "merge_request"}
	p.ObjectAttributes.Action = action
	p.ObjectAttributes.Draft = draft
	p.ObjectAttributes.TargetProjectID = projectID
	p.ObjectAttributes.IID = iid
	return p
}

// 模拟真实流程：open(draft) -> update(ready) 应触发
func TestStateTrackerDraftToReady(t *testing.T) {
	tracker := newStateTracker("", testLogger())

	// 创建草稿 MR
	if r := tracker.Evaluate(newPayload("open", true, 7, 10)); r.Triggered {
		t.Fatalf("open 事件不应触发: %+v", r)
	}
	// Mark as ready
	r := tracker.Evaluate(newPayload("update", false, 7, 10))
	if !r.Triggered {
		t.Fatalf("期望触发，实际: %+v", r)
	}
}

// 冷启动：首次见到 update 事件（无 open 记录）不应触发
func TestStateTrackerColdStartNoTrigger(t *testing.T) {
	tracker := newStateTracker("", testLogger())
	r := tracker.Evaluate(newPayload("update", false, 7, 10))
	if r.Triggered {
		t.Fatalf("冷启动首次事件不应触发: %+v", r)
	}
}

func TestStateTrackerNotMergeRequest(t *testing.T) {
	tracker := newStateTracker("", testLogger())
	p := newPayload("update", false, 7, 10)
	p.ObjectKind = "push"
	if r := tracker.Evaluate(p); r.Triggered {
		t.Fatalf("非 MR 事件不应触发: %+v", r)
	}
}

// ready -> draft 不应触发
func TestStateTrackerReadyToDraftIgnored(t *testing.T) {
	tracker := newStateTracker("", testLogger())
	tracker.Evaluate(newPayload("open", false, 7, 10)) // 创建即 ready
	if r := tracker.Evaluate(newPayload("update", true, 7, 10)); r.Triggered {
		t.Fatalf("ready -> draft 不应触发: %+v", r)
	}
}

// draft 状态未变化不应触发
func TestStateTrackerDraftUnchangedIgnored(t *testing.T) {
	tracker := newStateTracker("", testLogger())
	tracker.Evaluate(newPayload("open", true, 7, 10))
	if r := tracker.Evaluate(newPayload("update", true, 7, 10)); r.Triggered {
		t.Fatalf("draft 未变化不应触发: %+v", r)
	}
}

// 多次切换：draft -> ready -> draft -> ready，第二次 ready 也应触发
func TestStateTrackerMultipleToggles(t *testing.T) {
	tracker := newStateTracker("", testLogger())
	tracker.Evaluate(newPayload("open", true, 7, 10)) // draft

	if r := tracker.Evaluate(newPayload("update", false, 7, 10)); !r.Triggered {
		t.Fatalf("第一次 ready 应触发: %+v", r)
	}
	tracker.Advance(newPayload("update", false, 7, 10))

	if r := tracker.Evaluate(newPayload("update", true, 7, 10)); r.Triggered {
		t.Fatalf("回到 draft 不应触发: %+v", r)
	}

	if r := tracker.Evaluate(newPayload("update", false, 7, 10)); !r.Triggered {
		t.Fatalf("第二次 ready 应触发: %+v", r)
	}
}

// close/merge 清除状态
func TestStateTrackerCloseClearsState(t *testing.T) {
	tracker := newStateTracker("", testLogger())
	tracker.Evaluate(newPayload("open", true, 7, 10))
	if r := tracker.Evaluate(newPayload("close", false, 7, 10)); r.Triggered {
		t.Fatalf("close 不应触发: %+v", r)
	}
	// 关闭后重新 open 应视为全新 MR，不触发
	if r := tracker.Evaluate(newPayload("open", false, 7, 10)); r.Triggered {
		t.Fatalf("close 后 open 不应触发: %+v", r)
	}
}

// 触发失败后状态不推进，重试可再次触发
func TestStateTrackerAdvanceOnlyOnSuccess(t *testing.T) {
	tracker := newStateTracker("", testLogger())
	tracker.Evaluate(newPayload("open", true, 7, 10))

	// 触发失败：不调用 Advance，状态仍为 draft
	if r := tracker.Evaluate(newPayload("update", false, 7, 10)); !r.Triggered {
		t.Fatalf("应触发: %+v", r)
	}
	// 不 Advance，重试同一事件仍应触发
	if r := tracker.Evaluate(newPayload("update", false, 7, 10)); !r.Triggered {
		t.Fatalf("未推进状态时重试应再次触发: %+v", r)
	}

	// 成功后推进，再次收到相同事件不再触发
	tracker.Advance(newPayload("update", false, 7, 10))
	if r := tracker.Evaluate(newPayload("update", false, 7, 10)); r.Triggered {
		t.Fatalf("推进状态后不应再触发: %+v", r)
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

func payloadJSON(action string, draft bool) []byte {
	payload := map[string]any{
		"object_kind": "merge_request",
		"object_attributes": map[string]any{
			"action":            action,
			"draft":             draft,
			"iid":               42,
			"title":             "feat: add hooks",
			"source_branch":     "feature/hooks",
			"target_project_id": 7,
		},
		"project": map[string]any{"id": 7},
	}
	b, _ := json.Marshal(payload)
	return b
}

// 完整流程：open(draft) -> update(ready) 触发流水线
func TestHandlerTriggersPipeline(t *testing.T) {
	fake := &fakeTriggerer{
		pipeline: &gitlab.Pipeline{ID: 123, Status: "pending", WebURL: "https://gitlab.example.com/pipe/123"},
	}
	h := NewHandler(fake, "secret", "", testLogger())

	// 创建草稿 MR
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payloadJSON("open", true)))
	req.Header.Set("X-Gitlab-Token", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("open 应返回 200 ignored，实际 %d", rec.Code)
	}

	// Mark as ready -> 触发
	req = httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payloadJSON("update", false)))
	req.Header.Set("X-Gitlab-Token", "secret")
	rec = httptest.NewRecorder()
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
	h := NewHandler(&fakeTriggerer{}, "secret", "", testLogger())

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payloadJSON("update", false)))
	req.Header.Set("X-Gitlab-Token", "wrong")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("期望 403，实际 %d", rec.Code)
	}
}

// 冷启动：无 open 记录直接收到 update(ready) 不应触发
func TestHandlerColdStartNoTrigger(t *testing.T) {
	fake := &fakeTriggerer{pipeline: &gitlab.Pipeline{ID: 1}}
	h := NewHandler(fake, "", "", testLogger())

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(payloadJSON("update", false)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("冷启动应返回 200 ignored，实际 %d", rec.Code)
	}
	if fake.calls != 0 {
		t.Fatalf("冷启动不应触发流水线，实际触发 %d 次", fake.calls)
	}
}

// --- 状态持久化测试 ---

// 重启后恢复状态：重启前 MR 为 draft，重启后首次事件是 ready 应触发
func TestStatePersistenceAcrossRestart(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "state.json")

	// 第一次运行：记录 draft=true（open 事件）
	tracker1 := newStateTracker(stateFile, testLogger())
	tracker1.Evaluate(newPayload("open", true, 7, 10))

	// 模拟重启：用同一状态文件创建新 tracker
	tracker2 := newStateTracker(stateFile, testLogger())

	// 重启后首次事件是 ready（update draft:false），应触发
	r := tracker2.Evaluate(newPayload("update", false, 7, 10))
	if !r.Triggered {
		t.Fatalf("重启后恢复状态应触发，实际: %+v", r)
	}
}

// 持久化推进：触发成功后状态写入文件，重启后不再重复触发
func TestStatePersistenceAdvance(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "state.json")

	tracker1 := newStateTracker(stateFile, testLogger())
	tracker1.Evaluate(newPayload("open", true, 7, 10))
	if r := tracker1.Evaluate(newPayload("update", false, 7, 10)); !r.Triggered {
		t.Fatalf("应触发: %+v", r)
	}
	tracker1.Advance(newPayload("update", false, 7, 10))

	// 重启后状态应为 false，再次收到 ready 事件不触发
	tracker2 := newStateTracker(stateFile, testLogger())
	if r := tracker2.Evaluate(newPayload("update", false, 7, 10)); r.Triggered {
		t.Fatalf("重启后不应重复触发: %+v", r)
	}
}

// 无状态文件时（冷启动）不触发，且不写文件
func TestStatePersistenceDisabled(t *testing.T) {
	tracker := newStateTracker("", testLogger())
	if r := tracker.Evaluate(newPayload("update", false, 7, 10)); r.Triggered {
		t.Fatalf("无持久化冷启动不应触发: %+v", r)
	}
}
