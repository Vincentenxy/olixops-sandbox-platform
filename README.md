# OlixOps Sandbox Platform

基于 OpenSandbox 的内部沙箱管理平台，为本地 AI 开发提供受控的执行环境。Go + Gin 后端，Vue 3 + TypeScript 控制台，PostgreSQL 持久化。

项目参考 `/Users/vincent/Downloads/env-vault.zip` 的分层、JWT 公钥验签、统一响应、配置和日志模块；不包含参考仓库的 Git 历史、示例公钥和运行凭据。

前后端采用同一仓库：Go 后端位于 `cmd/`、`internal/`，Vue 前端位于 `web/`，独立构建和部署。

项目改名后，JWT audience 与沙箱 metadata 的平台标识继续使用 `olixops-agent-platform`，以兼容已有令牌和远端资源查询；它们是稳定的内部标识，不跟随展示名称变更。

## 当前可运行范围

- 本地账号密码登录、bcrypt 密码哈希、RSA JWT 签发、公钥验签与 PostgreSQL 会话撤销，以 `staffuserid` 作为用户身份；预留公司 JWT/OIDC 接入。
- 项目创建与成员角色；普通成员只能查看、操作自己创建的沙箱；owner/maintainer 管理项目内沙箱；owner 管理成员。
- 沙箱创建、列表、状态同步、续期、销毁。固定规格 1 CPU / 1Gi，服务端镜像白名单与 TTL 上限。
- PostgreSQL 事务锁控制项目活跃沙箱配额；创建幂等键；不确定结果保持 Unknown 并保留配额。
- 前台/后台命令、命令日志、文件上传/下载/文本编辑/删除、目录查看。
- 每用户一个逻辑 Namespace；多个持久 Workspace、独立 PVC、受控镜像挂载、空闲归档；对话引用绑定工作区并查询原卷和历史。
- HTTP GET 接口验证，经后端授权转发；不将 OpenSandbox API Key 或原始 Endpoint 凭据发到浏览器。
- 项目审计、15 秒后台状态核对、标准错误结构与 trace ID。

**OIDC 先预留**：本版没有登录重定向、授权码换取令牌、JWKS 自动轮换或刷新令牌。当前页面使用本地账号密码登录。正式接入时优先复用公司现有登录入口，替换为平台会话，不把 ID Token 当成任意资源服务器的 Access Token。

**尚未实现**：工作区回收站和数据清除、完整 AI 执行上下文、资源规格自定义、批量队列、Pool、交互式 PTY/WebSocket、完整 Web 应用预览、镜像构建发布和多集群管理。未挂载工作区的临时沙箱文件随实例销毁；持久工作区只保留 `/workspace`。后续阶段见 `design/roadmap.md`。

PVC 持久化工作区的设计提案见 [场景、状态与开发方案](design/persistent-workspace-plan.md)，以及 [一页流程概览](design/persistent-workspace-overview.html)。

用户与对话绑定规则见 [产品设计草案（待确认）](design/workspace-product-draft.md)，参考依据见 [成熟产品官方资料调研](design/workspace-market-research.md)。设计已获确认，第一阶段实现和操作步骤见 [工作区第一阶段说明](design/workspace-phase1.md)。

## 1. 本地启动

要求 Docker Compose、Node.js 22.12+（或 20.19+）、Go 1.26。项目沿用参考工程的 Go 版本，支持 Go 自动下载工具链。

```bash
cd /Users/vincent/Desktop/codes.nosync/olixops-sandbox-platform
# 仅首次启动数据库；固定绑定本地 15439，使用独立命名卷
docker compose up -d postgres
# 生成本地 RSA 签名密钥（启动脚本也会在缺失时自动生成）
# 若终端已有不匹配的 GOROOT，必须去掉它
env -u GOROOT go run ./scripts/dev-auth.go
# 按需要设置 OpenSandbox Server API Key（不要加到前端环境变量）
# export SANDBOX_API_KEY='你的 OpenSandbox API Key'
./scripts/dev.sh
```

另开一个终端启动前端：

```bash
cd /Users/vincent/Desktop/codes.nosync/olixops-sandbox-platform
npm ci --prefix web
npm run dev --prefix web
```

打开 <http://127.0.0.1:5273>，输入账号 `vincent`。本机随机初始密码保存在 `.local/local-password.txt`：

```bash
pbcopy < .local/local-password.txt
```

将剪贴板密码粘贴到登录页即可。登录签发 8 小时 JWT，退出立即撤销当前会话；页面刷新可继续使用有效会话。启动脚本自动生成密码文件并首次创建账号，再次启动不会重置数据库中已有密码。旧的 `.local/vincent.token` 开发令牌在本地登录模式下不再授予访问权限。

完整说明和新增用户命令见 [本地认证说明](design/local-auth.md)。

使用顺序：新建项目 → 创建沙箱 → 等待 Running → 命令里执行 `mkdir -p /workspace` → 测试文件与命令。镜像 `python:3.12` 是初始白名单示例，需要确认集群能够拉取，可改为已验证的内网镜像。

持久化操作：进入“持久化工作区” → 新建工作区 → 启动 → 在实例内操作 `/workspace`；在“对话关联”登记外部对话 ID 并保存绑定。停止实例保留数据。当前工作区没有数据删除入口。

接口验证：启动应用监听 `0.0.0.0:8000`，在「接口验证」填写端口 8000、路径 `/health`。响应只作为文本显示，避免把沙箱 HTML 放在平台同源页面执行。这不是完整浏览器预览代理。

开发数据保存在 Docker 命名卷；`docker compose down` 停服务但保留数据。登录过期时重新输入账号密码。`.local/`、私钥、令牌均被 Git 忽略。

## 2. OpenSandbox 和认证配置

配置文件：`configs/config.yaml`。环境变量优先，例如：

| 环境变量 | 用途 |
|---|---|
| `SANDBOX_BASE_URL` | Server 根地址，含必要前缀，例如 `http://efficient-lab.qiuer.net/osb`，不要额外加 `/v1` |
| `SANDBOX_API_KEY` | 只由后端使用的 API Key |
| `AUTH_JWT_PUBLIC_KEY` | 外部 JWT 模式的公司 RSA 公钥；本地模式从签名私钥推导公钥 |
| `AUTH_ISSUER` / `AUTH_AUDIENCE` | 签发者与接收方校验；配置必须与公司 JWT 契约一致 |
| `DATABASE_DSN` | PostgreSQL 连接串 |
| `SERVER_HOST` / `SERVER_PORT` | 默认 127.0.0.1:18090；容器中设置 SERVER_HOST=0.0.0.0 |

后端 SDK 固定为 `github.com/alibaba/OpenSandbox/sdks/sandbox/go v1.2.0`，本版仅使用基础生命周期/execd 接口；SDK 支持的其他新接口不代表当前集群支持。远端请求默认不继承本机 HTTP_PROXY，以免重复遇到 VPN/代理路径问题；TLS 证书验证保持开启。若确实需要代理，应专门配置 transport。

所有数据面请求通过配置的 Server 地址和 `/sandboxes/{id}/proxy/{port}` 路径发送。即使上游 Endpoint 返回其他域名，也不将后端凭据跟随到任意目标。

开发机 workspace 配置显式指向 `poc-cluster` 的 `opensandbox` Namespace；生产需改为专用 ServiceAccount（kubeconfig/context 置空），不要携带本机管理员凭据。第一阶段建表 SQL 为 `migrations/003_workspaces.sql`。详见 [第一阶段说明](design/workspace-phase1.md)。

## 3. 权限与部署边界

详见 `design/security.md`。权限来源是数据库项目成员关系与服务端管理员配置，不采信请求传入的 owner、role、backendId。

若普通员工仍能直接访问使用平台共享凭据的 OpenSandbox Server API 或代理入口，就能绕过平台授权。实际共享部署应由网络/网关限制后端入口只允许平台访问，保留受控运维通道。此项目没有替你修改现有 Higress 或集群访问策略。

## 4. 测试与构建

```bash
# 单元及集成测试；集成测试在同库独立随机 schema 中运行，结束后删除自己的 schema
TEST_DATABASE_DSN='postgres://olixops:olixops-local@127.0.0.1:15439/olixops?sslmode=disable' \
  env -u GOROOT go test -race ./...
env -u GOROOT go vet ./...
env -u GOROOT go build -o bin/server ./cmd
npm run build --prefix web
npm audit --prefix web
# swaggo 输出 docs/，独立 OpenAPI 在 api/openapi.json
make swagger
```

未设置 `TEST_DATABASE_DSN` 时，数据库集成测试会明确 Skip；上面的显式命令才会执行完整测试。集成测试使用模拟 OpenSandbox HTTP 服务，不在真实 K8s 集群创建实例。本轮另外通过 scripts/smoke.py 在现有集群完成真实创建、命令、文件、HTTP 服务访问与销毁联调，详见 design/validation.md。上线前仍需验证生产配置。

## 5. 目录

```text
cmd/                         服务入口
configs/                     Viper 配置
internal/domain/             资源模型与权限语义
internal/application/        创建编排与状态核对
internal/infrastructure/     PostgreSQL 与 OpenSandbox SDK 适配
internal/interfaces/         Gin handler / router / middleware
pkg/                         参考项目的日志、响应和用户上下文
migrations/                  数据库 SQL
api/                         独立 OpenAPI 文档
web/                         Vue 控制台
scripts/                     本地开发密钥和启动脚本
design/                      数据库、权限、架构与迭代计划
```

## 6. 本轮验收记录

见 [design/validation.md](design/validation.md)。真实 smoke 是可选验收命令，不是启动前置步骤：

```bash
python3 scripts/smoke.py --real
```

## 7. 继续对话与恢复运行（第二阶段）

在「持久化工作区 → 对话关联」登记对话并绑定工作区，点击「继续使用」。后台准备状态会持久保存；就绪后点击「进入实例」执行命令、操作文件或验证 HTTP 接口。

可用旧实例会复用；销毁实例并等待清理后，再次继续会挂载同一个 PVC。文件写在 `/workspace` 内才会保留。依赖包、后台进程和临时可写层不自动恢复。结束执行只使旧上下文失效，仍需在实例页销毁不需要的计算。

新增接口为 `conversation/continue`、`run/status`、`run/list`、`run/release`；工具请求增加 `runContextId`。详见 [接口与操作说明](design/conversation-runtime-phase2.md) 和 [OpenAPI](api/openapi.json)。PostgreSQL 迁移按 `001` → `004` 执行；`database.migrate=true` 的本地启动会自动应用。

真实集群恢复验收（创建两个短期实例，最终保留测试 PVC 和文件、清理实例）：

```bash
python3 scripts/runtime-smoke.py --real
```
