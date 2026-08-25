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
- 通过**状态机跟踪** `object_attributes.draft` 精确判定 "Mark as ready"，不会误触发其他 MR 更新
- Webhook Secret 校验（`X-Gitlab-Token`，恒定时间比较）
- 私有化 GitLab：地址、端口、令牌均通过 **YAML 配置文件** 提供
- 结构化日志（JSON）、优雅关闭、Docker 部署

## 版本要求

本项目依赖 GitLab webhook 中的 `changes.draft` 字段，该字段自 **GitLab 17.10**
（`CI_MERGE_REQUEST_DRAFT` 变量引入的版本）起稳定存在。低于 17.10 的版本不受支持。

## 配置文件

通过 `-config` 参数指定配置文件路径（默认 `./config.yaml`）。参考
[`config.example.yaml`](./config.example.yaml)：

```yaml
# HTTP 监听配置
listen:
  addr: ":9932"

# GitLab 连接配置
gitlab:
  base_url: "https://gitlab.com"              # 可选，默认公有 GitLab；私有化填根地址含端口
  token: "glpat-xxxxxxxxxxxxxxxx"             # 访问令牌（需 api 权限）
  insecure_skip_verify: false                 # 跳过 TLS 校验（自签名/无 IP SAN 证书场景）
  ca_cert_file: ""                            # 自定义 CA 证书路径（PEM），与上者互斥

# Webhook 校验配置
webhook:
  secret: ""                                    # 与 GitLab Webhook Secret 一致；留空不校验

# 状态持久化文件路径。网关重启后恢复 MR 草稿状态，避免冷启动误判。
# 留空则不持久化（仅内存跟踪）。
state_file: "state.json"

# 调用 GitLab API 的超时时间
pipeline_timeout: 30s

# Webhook 请求体上限（字节），默认 10MB
max_body_bytes: 10485760
```

| 配置项 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `listen.addr` | ❌ | `:9932` | 网关 HTTP 监听地址 |
| `gitlab.base_url` | ❌ | `https://gitlab.com` | GitLab 根地址，含端口；私有化如 `http://gitlab.example.com:8929` |
| `gitlab.token` | ✅ | — | GitLab 访问令牌，需 **Developer+ 角色** 且勾选 `api` scope（见下方"令牌要求"） |
| `gitlab.insecure_skip_verify` | ❌ | `false` | 跳过 TLS 证书校验（自签名/证书无 IP SAN 场景），仅建议内网使用 |
| `gitlab.ca_cert_file` | ❌ | 空 | 自定义 CA 证书路径（PEM），GitLab 用内部 CA 签发证书时配置 |
| `webhook.secret` | ❌ | 空（不校验） | Webhook Secret token，建议配置 |
| `state_file` | ❌ | `state.json` | 状态持久化文件；留空则不持久化 |
| `pipeline_timeout` | ❌ | `30s` | GitLab API 调用超时（如 `10s`、`1m`） |
| `max_body_bytes` | ❌ | `10485760` | Webhook 请求体上限（字节） |

## 令牌要求（重要）

`gitlab.token` 用于调用 GitLab API 创建 MR 流水线，**必须同时满足**：

1. **令牌类型**：个人访问令牌（PAT）或项目访问令牌均可。
   - 个人访问令牌：用户设置 → Access Tokens 创建，归属你自己。
   - 项目访问令牌：项目 → Settings → Access Tokens 创建，归属一个 bot 用户
     （流水线 "Created by" 会显示该 bot，而非创建者）。
2. **角色（Role）**：**必须为 Developer（开发者）或更高**（Maintainer/Owner）。
   - 项目访问令牌默认是 **Guest** 角色，Guest/Reporter **没有创建流水线的权限**，
     会报 `400 Insufficient permissions to create a new pipeline`。
   - 创建/编辑令牌时，将 Role 设为 **Developer**。
3. **权限范围（Scope）**：必须勾选 **`api`**（完整 API 访问）。
   - 仅 `read_api` 或 `read_repository` 不足以创建流水线。

> 排查：若触发时报 `400 Insufficient permissions to create a new pipeline`，
> 请检查令牌角色是否为 Developer+；若报 `401`，检查令牌是否有效及 `api` scope。

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
docker run -d -p 9932:9932 \
  -v "$PWD/config.yaml:/etc/gateway/config.yaml:ro" \
  --name mr-hooks mr-hooks-gateway
```

> 镜像默认从 `/etc/gateway/config.yaml` 读取配置，通过挂载覆盖即可。

健康检查：`curl http://localhost:9932/healthz` → `{"status":"ok"}`

### 2. 配置 GitLab Webhook

项目 → **Settings → Webhooks**（或群组级 Webhook）：

- **URL**: `http://<网关地址>:9932/webhook`
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

网关通过**状态机**跟踪每个 MR 的草稿状态（`object_attributes.draft`），
检测 `draft: true → false` 的转换来判定 "Mark as ready"：

| 事件 | 行为 |
| --- | --- |
| 首次见到某 MR（冷启动） | 记录当前 draft，**不触发** |
| `open` / `reopen` | 记录当前 draft，不触发 |
| `update` 且上次 `true`、当前 `false` | **触发流水线** |
| `update` 其它情况 | 仅更新状态，不触发 |
| `close` / `merge` | 清除状态，不触发 |

> 说明：GitLab webhook 的 `changes.draft` 字段并不可靠（实测在 "Mark as ready"
> 时可能缺失或报告 `from:false,to:false`），因此改为跟踪可靠的
> `object_attributes.draft` 字段推导状态转换。

触发成功后才会推进内部状态，因此若 GitLab API 调用失败，GitLab 重试时会再次尝试触发。

**状态持久化**：状态会写入 `state_file` 指定的 JSON 文件（默认 `state.json`）。
网关重启后自动恢复历史状态，避免冷启动误判——例如重启前 MR 为草稿、重启后
首次事件是 ready，若状态丢失会漏触发。真正的冷启动（无状态文件）首次事件
仅记录状态、不触发，避免对已就绪 MR 的频繁更新误触发流水线。

## 常见问题

**Q: 触发流水线后 GitLab 没有 Job 运行？**
A: 网关只负责"创建 MR Pipeline"。Job 是否执行取决于项目的 `.gitlab-ci.yml` 中
`workflow: rules` 是否允许 MR pipeline（如 `if: $CI_PIPELINE_SOURCE == "merge_request_event"`）。

**Q: 400 / 403 错误？**
A: 确认 `config.yaml` 中 `gitlab.token` 具备 **Developer+ 角色** 且勾选 **`api`** scope，
且属于该项目或更高层级。项目访问令牌默认是 Guest 角色，需手动改为 Developer。

**Q: 如何确认网关收到了事件？**
A: 在 GitLab Webhook 页面点击 "Test"，网关日志会输出收到的事件与判定结果。

## 开发

```bash
make test    # 单元测试
make vet     # 静态检查
make build   # 一键编译到 output/（含配置文件）
```

### 一键编译

编译二进制并连同配置文件一起输出到 `output/` 目录：

```bash
./build.sh                 # 编译当前平台
./build.sh linux amd64     # 交叉编译指定平台/架构
make build                 # 等价于 ./build.sh
```

产物结构：

```
output/
├── gitlab-ci-mr-pipelines-hooks   # 可执行文件
└── config.yaml                    # 配置文件（由 config.example.yaml 复制）
```

运行：

```bash
./output/gitlab-ci-mr-pipelines-hooks -config output/config.yaml
```

## 许可证

MIT
