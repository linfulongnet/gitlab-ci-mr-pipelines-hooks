# GitLab MR Pipelines Hooks

一个轻量的 GitLab Merge Request Webhook 网关。监听 MR 从 **Draft（草稿）** 变为 **Ready（就绪）**，
自动调用 GitLab API 为该 MR 触发一次流水线，让 Go CI、Flutter CI 等测试 Job 在点击 "Mark as ready" 后才开始执行。

```
最后一次 Push
     │
     ▼
Draft MR
     │
     │  不触发最终 CI
     ▼
点击 Mark as ready
     │
     ▼
Merge Request Webhook (→ 本网关)
     │
     │  检测 changes.draft: true → false
     ▼
POST /api/v4/projects/:id/merge_requests/:iid/pipelines
     │
     ▼
MR Pipeline
     ├── Go CI
     ├── Flutter CI
     └── 其它 MR 检查
```

## 特性

- 仅使用 Go 标准库，零第三方依赖，单二进制部署
- 通过 `changes.draft: true → false` 精确判定 "Mark as ready"，不会误触发其他 MR 更新
- Webhook Secret 校验（`X-Gitlab-Token`，恒定时间比较）
- 私有化 GitLab：地址、端口、令牌均可配置
- 结构化日志（JSON）、优雅关闭、Docker 部署
- 兼容模式 `TRIGGER_ON_UPDATE`：适配不携带 `changes.draft` 的旧版 GitLab

## 环境变量

| 变量 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `GITLAB_BASE_URL` | ✅ | — | 私有化 GitLab 根地址，含端口，如 `http://gitlab.example.com:8929` |
| `GITLAB_TOKEN` | ✅ | — | GitLab 访问令牌（Personal/Project Access Token，需勾选 `api` 权限） |
| `GITLAB_WEBHOOK_SECRET` | ❌ | 空（不校验） | Webhook Secret token，建议配置 |
| `LISTEN_ADDR` | ❌ | `:8080` | 网关 HTTP 监听地址 |
| `TRIGGER_ON_UPDATE` | ❌ | `false` | 兼容旧版 GitLab：对任意 `draft=false` 的 MR 更新也触发 |
| `PIPELINE_TIMEOUT` | ❌ | `30s` | GitLab API 调用超时（如 `10s`、`1m`） |
| `MAX_BODY_BYTES` | ❌ | `10485760` | Webhook 请求体上限（字节） |

## 快速开始

### 1. 构建与运行

```bash
go build -o gateway .
GITLAB_BASE_URL=http://gitlab.example.com:8929 \
GITLAB_TOKEN=glpat-xxxx \
GITLAB_WEBHOOK_SECRET=my-secret \
LISTEN_ADDR=:8080 \
./gateway
```

或使用 Docker：

```bash
docker build -t mr-hooks-gateway .
docker run -d -p 8080:8080 \
  -e GITLAB_BASE_URL=http://gitlab.example.com:8929 \
  -e GITLAB_TOKEN=glpat-xxxx \
  -e GITLAB_WEBHOOK_SECRET=my-secret \
  --name mr-hooks mr-hooks-gateway
```

健康检查：`curl http://localhost:8080/healthz` → `{"status":"ok"}`

### 2. 配置 GitLab Webhook

项目 → **Settings → Webhooks**（或群组级 Webhook）：

- **URL**: `http://<网关地址>:8080/webhook`
- **Secret token**: 与 `GITLAB_WEBHOOK_SECRET` 一致
- **Trigger**: 勾选 **Merge request events**
- 保存后点击 **Test** 验证连通性

> 注意：GitLab 侧需要能被网关访问；若走公网建议置于 HTTPS 反向代理之后。

### 3. 触发流程验证

在 GitLab 创建 Draft MR → 点击 **Mark as ready**，观察网关日志：

```json
{"level":"INFO","msg":"收到 Merge Request Webhook 事件","mr_iid":42,"action":"update","draft":false,...}
{"level":"INFO","msg":"事件判定","triggered":true,"reason":"检测到 draft: true -> false（Mark as ready）"}
{"level":"INFO","msg":"已触发 MR 流水线","project_id":7,"mr_iid":42,"pipeline_id":123,"status":"pending",...}
```

## 判定逻辑

严格模式（默认）仅在同时满足以下条件时触发：

1. `object_kind == "merge_request"`
2. `object_attributes.action == "update"`
3. `changes.draft.from == true` 且 `changes.draft.to == false`

任一不满足都会被忽略并返回 `200 {"status":"ignored"}`，不会干扰 GitLab 重试。

**兼容旧版 GitLab**：若你的 GitLab 版本过旧、webhook 中不含 `changes.draft` 字段，
可设置 `TRIGGER_ON_UPDATE=true`——此时任何非草稿 MR 的 update 事件都会触发流水线
（注意：这会放大触发范围，建议仅在确认旧版本后开启）。

## 常见问题

**Q: 触发流水线后 GitLab 没有 Job 运行？**
A: 网关只负责"创建 MR Pipeline"。Job 是否执行取决于项目的 `.gitlab-ci.yml` 中
`workflow: rules` 是否允许 MR pipeline（如 `if: $CI_PIPELINE_SOURCE == "merge_request_event"`）。

**Q: 409 / 403 错误？**
A: 确认 `GITLAB_TOKEN` 具备 `api` 权限，且属于该项目或更高层级。

**Q: 如何确认网关收到了事件？**
A: 在 GitLab Webhook 页面点击 "Test"，网关日志会输出收到的事件与判定结果。

## 开发

```bash
make test    # 单元测试
make vet     # 静态检查
make build   # 编译
```

## 许可证

MIT
