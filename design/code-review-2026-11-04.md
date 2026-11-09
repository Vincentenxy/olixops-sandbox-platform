# 代码审核与修复记录（2026-11-04）

## 范围与结论

审核对象为 `olixops-agent-platform` 当前全部源码：Go 后端、本地认证、项目授权、OpenSandbox 代理、持久化工作区、对话关联、PostgreSQL SQL 和 Vue 前端。

仓库尚无 Git 提交，不能用历史提交差异表示审核范围。本次按照项目 `AGENT.md`、已确认的工作区设计和 `design/workspace-phase1.md` 审核当前工作目录。规范与需求分别进行独立只读审查，主审负责修复和执行验证；修复后再次独立复审。

共处理 10 项问题：需求实现 4 项、规范及交互 5 项、后台核对可用性 1 项。P1 表示应优先修复的资源或持久化风险，P2 表示功能、并发或追踪准确性问题，P3 表示接口契约问题。最终复审未发现本次修复新增的阻断问题；这不等同于全面的生产安全认证或压力测试。

## 需求实现审核（Spec）

| 严重性 | 问题与触发方式 | 修复结果 | 主要位置 |
|---|---|---|---|
| P1 | 同步请求持有旧 `Creating / PVCUID` 为空的快照；另一请求已保存原 UID，原 PVC 随后被删除；旧请求可能按旧状态创建同名空 PVC，数据库拒绝新 UID 时已经太迟。 | 用一个数据库事务锁定并重新读取工作区，执行存储核对后在同一事务保存观察结果。远端实例和消费者核验在提交后执行。旧请求不能再以旧状态重建卷。 | `internal/application/workspace.go`、`internal/infrastructure/store/workspace.go` |
| P2 | 归档先锁工作区，审计外键再等待项目锁；启动或绑定先锁项目、再等工作区，形成锁环。 | 归档统一采用项目 → 工作区的加锁顺序。 | `internal/infrastructure/store/workspace.go` |
| P2 | 管理者代成员启动工作区，沙箱 owner 是成员，创建审计和 create_by 也错误使用成员身份。 | 应用层和存储层显式传递实际 actor；资源 owner 保持原成员，审计、创建人和更新人记录真实操作者。 | `internal/interfaces/handler/platform.go`、`internal/application/platform.go`、`internal/infrastructure/store/postgres.go` |
| P2 | 确定性创建失败后，记录为 Failed 且无 backend ID；三分钟后同步会将零匹配结果改成 Unknown，重新占用配额且无法正常删除。 | 同步直接跳过终态；数据库更新也保护 Failed，抵御迟到写回。真实远端 Failed 仍映射到 Error，以保留远端资源核验语义。 | `internal/application/platform.go`、`internal/infrastructure/store/postgres.go` |

需求轴：4 项均已修复；最高严重性为旧快照可能误建空 PVC。

## 规范与实现审核（Standards）

| 严重性 | 问题与影响 | 修复结果 | 主要位置 |
|---|---|---|---|
| P1 | SDK 后台日志全量 `io.ReadAll` 无大小限制。进一步复审确认，命令 SSE 解析器也会在业务回调之前无上限缓存完整一行。 | 所有 SDK 响应在读取过程中限制为 8 MiB，日志限制为 1 MiB；同时检查 Content-Length 和 chunked 响应。命令业务输出继续限制为 1 MiB。超限统一返回标准 HTTP 413。日志支持非负字节 cursor，前端成功读取后更新游标。 | `internal/infrastructure/sandbox/client.go`、`internal/interfaces/handler/platform.go`、`web/src/App.vue` |
| P2 | 工作区请求进行中切换项目，新请求被 busy 丢弃；旧对话定位结果也可能回填新项目页面。 | 工作区组件以项目 ID 为 key，新项目独立加载；卸载标记屏蔽旧实例的迟到错误，旧实例结果无法覆盖新实例页面。 | `web/src/App.vue`、`web/src/Workspaces.vue` |
| P2 | 600px 以下侧栏完全隐藏，持久化工作区、成员和审计失去入口。 | 增加移动端页面导航选择器，保留桌面导航并补齐无障碍名称。 | `web/src/App.vue`、`web/src/style.css` |
| P3 | `AGENT.md` 规定用户上下文包含请求 Cookie，但认证中间件未保存。 | 内部上下文补齐 Cookie，使用 `json:"-"`，不会向用户资料接口输出凭证。 | `internal/interfaces/middleware/auth.go`、`pkg/userctx/userctx.go` |
| P3 | 直接返回 SDK 类型，命令事件为 Event/Data/ID、日志为 Output/Cursor、文件时间为下划线格式，不符合平台小驼峰契约。 | 统一映射成 event/data/id、output/cursor、modifiedAt/createdAt；同步维护日志请求与 413 的 OpenAPI 描述。 | `internal/interfaces/handler/platform.go`、`api/openapi.json` |

规范轴：5 项均已修复；最高严重性为 SDK 读取和 SSE 解析之前没有内存边界。

## 主审补充：后台核对可用性

P2：原来同一后台循环先串行处理最多 100 个工作区，再处理沙箱。存储 API 每次等待超时可能让删除重试和沙箱状态核对长时间排在后面。

现在将存储核对和沙箱生命周期核对放入独立循环。存储使用固定 4 个 worker，每个任务有 20 秒截止时间，同一循环的批次不会重叠。存储异常不再阻挡沙箱核对，取消上下文时两个循环都会退出。

另将从工作区创建沙箱后的页面切换到沙箱列表并选中新实例，使后续命令、文件和接口验证入口直接可见。

## 回归验证

### 自动化

使用真实 PostgreSQL，通过 `TEST_DATABASE_DSN` 运行测试；每个集成测试创建自己的随机 schema 并在结束时清理，不在业务表里准备测试用户或资源。

```bash
TEST_DATABASE_DSN='<测试 PostgreSQL DSN>' env -u GOROOT go test -race ./...
env -u GOROOT go vet ./...
env -u GOROOT go build -o .local/review-server ./cmd
npm run build --prefix web
make swagger
```

新增或增强的验证包括：

- 多个并发旧快照在原 PVC 消失后，只能保留原 UID 并进入 Error，不创建替代空卷。
- 在真实 PostgreSQL 持有项目锁，让归档确实进入锁等待，再用 NOWAIT 核对工作区没有被反向持锁。
- 管理者代成员启动时，资源 owner 与实际审计 actor 分别正确。
- Failed 记录经过三分钟后的同步和迟到 Unknown 写回仍保持 Failed，新的实例仍可使用配额。
- 存储 API 持续阻塞时，首轮定时触发仍能进行沙箱生命周期核对；取消后 worker 能退出。
- 已知 Content-Length 和 chunked 日志都受 1 MiB 上限约束；恰好 1 MiB 可以读取，cursor 正确返回。
- 单行、无换行、超过 8 MiB 的命令 SSE，在业务回调没有主动拒绝时也必须被底层读取上限阻断。
- HTTP 日志接口支持 cursor、拒绝负值、返回小驼峰字段，超限使用标准 413 响应。
- Cookie 内部上下文可用，外部用户 JSON 不包含 Cookie/JWT。
- 原有 JWT、退出失效、跨用户授权、并发配额、幂等、未知创建结果恢复、路径防绕过和 PVC 消费者核验测试继续通过。

### 浏览器与本机运行

- 1440px 桌面：人为挂起旧项目的工作区请求，切换另一个项目，验证新项目立即加载；释放旧请求后不能覆盖新项目内容。此项使用浏览器拦截的测试响应，不写入真实工作区或对话数据。
- 390px 手机宽度：验证页面导航能进入成员和持久化工作区页面，文档宽度与视口均为 390px，无页面横向溢出。
- 本地后端更新后重新验证健康检查、登录、项目查询和原工作区查询；浏览器实际打开长期代码验收工作区并定位对话，返回的仍是原 PVC UID。
- 对已有验收工作区只读核对 Kubernetes PVC，确认原 UID `9401565f-f8f1-45d5-8448-2348a23782da` 保留，PVC 为 Bound，数据库中的 PV 名称与集群一致。

本轮没有新增或删除真实 PVC，没有执行生产部署。本轮没有修改数据库表结构，不需要额外数据库迁移。测试和构建原始输出保存在被 Git 忽略的 `.local/review-*.log`。

## 使用变化与边界

日志读取现在默认为 cursor=0，成功响应为 `{output, cursor}`，下一次可以从返回的 cursor 继续读取。超限响应不会前移前端游标；已有巨量历史日志应选择合适的字节游标，或先在沙箱内使用 tail/grep 筛选。限制 HTTP 响应不等于停止产生大量日志的沙箱进程，也不限制其磁盘用量。

前台命令事件和文件时间字段已按平台规范调整；如果已有自写客户端按旧 SDK 大写或下划线字段取值，需要一起更新。

此次验证覆盖单机平台进程、真实 PostgreSQL 并发和 Kubernetes 的只读身份核对。未执行生产负载压力测试、多平台副本故障注入、真实节点故障恢复或 PVC 备份恢复演练。原 StorageClass `local-path` 的节点依赖、容量硬限制和底层回收策略不由这些代码修复改变。
