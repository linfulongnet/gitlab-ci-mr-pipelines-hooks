package webhook

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// mrState 记录某个 MR 的已知草稿状态。
type mrState struct {
	Draft    bool      `json:"draft"`
	LastSeen time.Time `json:"last_seen"`
}

// stateTracker 跟踪每个 MR 的草稿状态，用于检测 draft -> ready 转换。
//
// 说明：GitLab webhook 的 changes.Draft 字段并不可靠（实测在"Mark as ready"
// 时可能缺失或报告 from:false,to:false），因此改为跟踪 object_attributes.Draft
// 这一可靠字段，通过状态机推导状态转换。
//
// 状态可持久化到 JSON 文件：网关重启后恢复历史状态，避免冷启动误判
// （例如重启前 MR 为 draft，重启后首次事件是 ready，若状态丢失会漏触发）。
type stateTracker struct {
	mu   sync.Mutex
	seen map[string]*mrState
	file string
	log  *slog.Logger
}

// newStateTracker 创建状态跟踪器。file 非空时从该文件加载持久化状态。
func newStateTracker(file string, log *slog.Logger) *stateTracker {
	t := &stateTracker{
		seen: make(map[string]*mrState),
		file: file,
		log:  log,
	}
	t.load()
	return t
}

func mrKey(projectID, iid int) string {
	return strconv.Itoa(projectID) + ":" + strconv.Itoa(iid)
}

// load 从文件加载持久化状态。文件不存在或解析失败时静默降级为内存模式。
func (t *stateTracker) load() {
	if t.file == "" {
		return
	}
	data, err := os.ReadFile(t.file)
	if err != nil {
		if !os.IsNotExist(err) {
			t.log.Warn("读取状态文件失败", "file", t.file, "err", err)
		}
		return
	}
	if err := json.Unmarshal(data, &t.seen); err != nil {
		t.log.Warn("解析状态文件失败", "file", t.file, "err", err)
		return
	}
	t.log.Info("已加载持久化状态", "file", t.file, "entries", len(t.seen))
}

// save 将状态原子写入文件（临时文件 + 重命名），避免写一半损坏。
// 写入失败仅告警，不影响内存状态跟踪。
func (t *stateTracker) save() {
	if t.file == "" {
		return
	}
	data, err := json.Marshal(t.seen)
	if err != nil {
		t.log.Warn("序列化状态失败", "err", err)
		return
	}
	if dir := filepath.Dir(t.file); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.log.Warn("创建状态目录失败", "dir", dir, "err", err)
			return
		}
	}
	tmp := t.file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		t.log.Warn("写入状态文件失败", "file", t.file, "err", err)
		return
	}
	if err := os.Rename(tmp, t.file); err != nil {
		t.log.Warn("更新状态文件失败", "file", t.file, "err", err)
	}
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
		t.save()
		return ReadyTransition{false, "action 为 " + attrs.Action + "，清除状态，忽略"}

	case "open", "reopen":
		t.seen[key] = &mrState{Draft: attrs.Draft, LastSeen: time.Now()}
		t.save()
		return ReadyTransition{false, "action 为 " + attrs.Action + "，记录 draft=" + boolStr(attrs.Draft) + "，忽略"}

	case "update":
		if !exists {
			// 冷启动：首次见到该 MR，仅记录，不触发
			t.seen[key] = &mrState{Draft: attrs.Draft, LastSeen: time.Now()}
			t.save()
			return ReadyTransition{false, "首次见到该 MR（冷启动），记录 draft=" + boolStr(attrs.Draft) + "，不触发"}
		}
		if prev.Draft && !attrs.Draft {
			// 检测到 draft: true -> false，触发；状态推进由 Advance 完成
			return ReadyTransition{true, "检测到 draft: true -> false（Mark as ready）"}
		}
		prev.Draft = attrs.Draft
		prev.LastSeen = time.Now()
		t.save()
		return ReadyTransition{false, "draft 状态未发生 true->false 转换（prev=" + boolStr(prev.Draft) + "），忽略"}

	default:
		// 其它 action（approved 等）：仅在没有记录时记录，不触发
		if !exists {
			t.seen[key] = &mrState{Draft: attrs.Draft, LastSeen: time.Now()}
			t.save()
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
		s.Draft = attrs.Draft
		s.LastSeen = time.Now()
		t.save()
	}
}
