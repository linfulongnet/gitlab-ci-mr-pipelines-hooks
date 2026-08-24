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

- 仅使用 Go 标准库 + `gopkg.in/yaml.v3`，单二进制部署
- 通过 `changes.draft: true → false` 精确判定 "Mark as ready"，不会误触发其他 MR 更新
- Webhook Secret 校验（`X-Gitlab-Token`，恒定时间比较）
- 私有化 GitLab：地址、端口、令牌均通过 **YAML 配置文件** 提供
- 结构化日志（JSON）、优雅关闭、Docker 部署
- 仅支持 GitLab **17.10 及以上**（与 `CI_MERGE_REQUEST_DRAFT` 变量引入版本一致）

## 版本要求

本项目依赖 GitLab webhook 中的 `changes.draft` 字段，该字段自 **GitLab 17.10**
（`CI_MERGE_REQUEST_DRAFT` 变量引入的版本）起稳定存在。低于 17.10 的版本不受支持。

## 配置文件

通过 `-config` 参数指定配置文件路径（默认 `./config.yaml`）。参考
[`config.example.yaml`](./config.example.yaml)：

```yaml
# HTTP 监听配置
listen:
  addr: ":8080"

# 私有化 GitLab 连接配置
gitlab:
  base_url: "http://gitlab.example.com:8929"   # 根地址，含端口
  token: "glpat-xxxxxxxxxxxxxxxx"               # 访问令牌（需 api 权限）

# Webhook 校验配置
webhook:
  secret: ""                                    # 与 GitLab Webhook Secret 一致；留空不校验

# 调用 GitLab API 的超时时间
pipeline_timeout: 30s

# Webhook 请求体上限（字节），默认 10MB
max_body_bytes: 10485760
```

| 配置项 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `listen.addr` | ❌ | `:8080` | 网关 HTTP 监听地址 |
| `gitlab.base_url` | ✅ | — | 私有化 GitLab 根地址，含端口，如 `http://gitlab.example.com:8929` |
| `gitlab.token` | ✅ | — | GitLab 访问令牌（Personal/Project Access Token，需勾选 `api` 权限） |
| `webhook.secret` | ❌ | 空（不校验） | Webhook Secret token，建议配置 |
| `pipeline_timeout` | ❌ | `30s` | GitLab API 调用超时（如 `10s`、`1m`） |
| `max_body_bytes` | ❌ | `10485760` | Webhook 请求体上限（字节） |

## 快速开始

### 1. 构建与运行

```bash
go build -o gateway .
cp config.example.yaml config.yaml
# 编辑 config.yaml，填入 gitlab.base_url 与 gitlab.token
./gateway -config config.yaml
```

或使用 Docker：

```bash
docker build -t mr-hooks-gateway .
docker run -d -p 8080:8080 \
  -v "$PWD/config.yaml:/etc/gateway/config.yaml:ro" \
  --name mr-hooks mr-hooks-gateway
```

> 镜像默认从 `/etc/gateway/config.yaml` 读取配置，通过挂载覆盖即可。

健康检查：`curl http://localhost:8080/healthz` → `{"status":"ok"}`

### 2. 配置 GitLab Webhook

项目 → **Settings → Webhooks**（或群组级 Webhook）：

- **URL**: `http://<网关地址>:8080/webhook`
- **Secret token**: 与 `config.yaml` 中 `webhook.secret` 一致
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

仅在同时满足以下条件时触发：

1. `object_kind == "merge_request"`
2. `object_attributes.action == "update"`
3. `changes.draft.from == true` 且 `changes.draft.to == false`

任一不满足都会被忽略并返回 `200 {"status":"ignored"}`，不会干扰 GitLab 重试。
若 `changes.draft` 缺失（GitLab 版本低于 17.10），同样忽略并记录日志。

## 常见问题

**Q: 触发流水线后 GitLab 没有 Job 运行？**
A: 网关只负责"创建 MR Pipeline"。Job 是否执行取决于项目的 `.gitlab-ci.yml` 中
`workflow: rules` 是否允许 MR pipeline（如 `if: $CI_PIPELINE_SOURCE == "merge_request_event"`）。

**Q: 409 / 403 错误？**
A: 确认 `config.yaml` 中 `gitlab.token` 具备 `api` 权限，且属于该项目或更高层级。

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
