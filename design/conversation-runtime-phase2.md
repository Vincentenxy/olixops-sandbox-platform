# 第二阶段：继续对话与恢复运行

实现范围：继续入口、持久准备任务、固定 RunContext、前端进度与进入实例、编号 PostgreSQL 迁移。沿用已确认工作区产品草案。聊天消息、OIDC、MCP 与镜像发布仍属后续阶段。

## 业务规则

- 继续时显式给出对话 ID、期望绑定版本、镜像、TTL 和请求幂等键。用户身份来自当前认证。
- 当前项目成员权限和对话/workspace owner 都要验证；管理者代操作记录真实 actor。
- 无绑定、绑定已变化、归档或另一个主动上下文占用，返回标准 409 和具体原因。
- 同一工作区最多一个未释放的 RunContext。同一操作者对同一对话、版本与配置重复继续返回原上下文；不同请求键也不产生第二个实例。
- 快照包含 conversationId、workspaceId、bindingVersion、sandboxId、attachmentGeneration、actor。每次操作重新检查认证、授权和快照，不能只凭上下文 ID 授权。
- 原实例 Running 且 execd 可用时复用；没有实例则挂载原 PVC 创建。旧实例仍有消费者、Unknown 或删除未完成时等待，不强行解锁。
- 准备任务有 Pending/Preparing/Waiting/Ready/Failed/Cancelled 状态、原因、重试时间、attempt 和数据库租约代次。准备期间没有浏览器也能继续处理。
- 外部创建之前先落库发送标记；标记存在就只核对元数据，不重放创建。崩溃发生在标记与发送之间也保守保持 Unknown。
- worker 用 SKIP LOCKED 领取任务；租约代次限制迟到更新。外部调用不持有准备任务事务。
- “结束本次执行”使上下文失效，不删除代码或自动终止后台进程。准备结果不确定时不允许结束并释放占用。销毁计算实例后等待旧消费者退出，随后允许继续生成新上下文。
- 工作区的工具操作串行；绑定、更换消费者、结束上下文与工具操作互斥。后台进程不属于前台请求互斥，页面明确提示仍可能写文件。

## 验收

1. 真实 PostgreSQL 集成测试：权限、重复请求、竞争、版本失效、租约接管、迟到 worker、创建结果未知与重启恢复。
2. SDK 与 Kubernetes stub 覆盖错误分支；全量 race、vet、Go build、前端类型检查/build。
3. 浏览器验证继续入口、状态、进入实例、执行和结束；窄屏可访问。
4. 可用的 PoC 集群上，从原工作区继续、写入文件、销毁实例、再次继续并读取同一文件；保留原 PVC，不清除历史代码。
5. 需求和规范分别独立复审，修复可复现问题后再交付。

## 使用与状态说明

在「持久化工作区 → 对话关联」登记并绑定自己的工作区，点击「继续使用」，选择受控镜像和新实例 TTL。页面显示准备进度；Ready 后通过「进入实例」操作文件和命令。实例已销毁时，旧上下文变为 Invalidated，再次继续创建新上下文和实例，原 PVC 保留。

| 执行状态 | 含义 | 用户操作 |
|---|---|---|
| Preparing | 已登记；后台等待领取或准备 | 等待进度，可关闭页面 |
| Waiting | 存储、配额、旧消费者或创建结果需要核对 | 按 message 处理，后台自动重试；有实例可查看并停止 |
| Ready | 固定实例 Running，执行服务可用 | 进入实例，或结束执行 |
| Failed | 权限、绑定、镜像等确定问题 | 修复原因后用新请求键重新继续 |
| Released | 用户结束本次执行 | 原上下文不能继续用于工具操作；再次继续可复用原实例 |
| Invalidated | 固定实例已终止 | 再次继续；旧消费者未退出时会等待 |

准备任务 operation.state 与执行状态分开：Pending/Preparing/Waiting/Ready/Failed/Cancelled。attempt 是累计领取核对次数，Ready 后仍有周期核对，不等同于失败重试次数。存储连接故障会保守拒绝数据操作；后台恢复后再允许使用。

`POST /api/v1/conversation/continue` 请求：

```json
{"conversationId":"平台对话ID","expectedVersion":1,"image":"python:3.12","ttl":600,"requestKey":"一次用户意图的稳定随机键"}
```

首次响应通常是 Preparing，不代表创建失败。用 `run/status` 携带 `{"id":"RunContext ID"}` 查询。文件/命令/日志/HTTP 验证继续使用 `sandbox/action`，附加 `runContextId` 与该上下文的 `sandboxId`。平台每次重新检查认证和资源授权，不能仅用 RunContext ID 获取访问权。

同一次网络重试使用相同 requestKey；新一轮执行使用新键。历史键永远返回原执行结果，即使该上下文已结束。一个工作区已有其他执行时返回 409；不会自动切走对话的绑定。执行列表显示全部未释放上下文以及最近 100 条历史；实例列表显示全部可见活跃实例及最近 200 条终态历史。

## 已知边界

- `/workspace` 文件通过 PVC 保留；系统层安装的依赖和运行进程不自动恢复。local-path 仍受节点故障影响，需要后续备份/恢复方案。
- 结束执行不会停止后台命令。前台 API 互斥不能保证任意后台程序对文件的并发写入安全。
- 上游创建没有可靠幂等保证时无法消除“已写发送标记但请求尚未发出”这一崩溃窗口。本版保守保留 Unknown 和占用，只按 operationId 核对，不自动重放；未提供强制解除未知占用的用户按钮。
- 暂不托管聊天消息、不提供平台 MCP 服务、不接入 OIDC，也不自动构建镜像。

## 2026-11-06 自测环境与结果

本机 Docker 引擎 API 返回 500，15439 的原开发数据库连接超时。本次安装本地 PostgreSQL 16，使用 `.local/phase2-pg`、127.0.0.1:15441、`olixops_phase2` 独立库。原 Docker 数据库及数据卷未修改。当前 5273/18090 开发服务使用该独立测试库，所以界面主要显示本次验收项目。

重启当前验收环境（仅本地）：

```bash
cd /Users/vincent/Desktop/codes.nosync/olixops-sandbox-platform
# 数据库已运行则跳过这一条；不注册系统自动启动服务。
/opt/homebrew/opt/postgresql@16/bin/pg_ctl -D .local/phase2-pg -l .local/phase2-pg.log -o '-h 127.0.0.1 -p 15441' start
DATABASE_DSN='postgres://vincent@127.0.0.1:15441/olixops_phase2?sslmode=disable' ./scripts/dev.sh
# 另一个终端
npm run dev --prefix web
```

恢复原 Docker 引擎后，正常开发可仍用 configs/config.yaml 的 15439 数据库，并应用 004 迁移；本次新验收记录不会自动复制到原库。

验证记录：

- 真实 PostgreSQL `go test -race ./...` 全量通过，router 集成测试约 68 秒；集成测试未跳过。独立随机 schema 随测试清理。
- 并发 continue、权限撤销、绑定版本变化、上下文/挂载代次、原卷消费者等待、创建响应丢失、租约接管和旧 worker 写回拒绝均有回归测试。
- 110 条执行历史、210 条沙箱历史不会挤出仍在运行的资源。
- `go vet ./...`、Go build、前端 vue-tsc/Vite build、swaggo 生成通过。SQL/内嵌 SQL/数据库文档一致。
- PoC 实际创建两个实例验证同一 PVC UID 原文件读取，另通过浏览器两轮实例验证继续/进入/执行/结束以及取消创建、同实例重选保留上下文。
- 浏览器 1440×960 和 390×844 检查通过；窄屏页面宽度为 390，表格在自身区域横向滚动。
- 四个验收实例最终均为 Deleted；测试工作区及文件保留。PVC `olx-ws-e05c78dc197e3da7ba96d3d0dcc70d03`，UID `a50fcb32-2f3d-451e-8a10-45994319fee4`，PV `pvc-a50fcb32-2f3d-451e-8a10-45994319fee4`。
- 本地证据：`.local/phase2-tests.log`、`.local/phase2-runtime-tests.log`、`.local/phase2-smoke.log`、`.local/runtime-smoke.json`、`.local/phase2-desktop.png`、`.local/phase2-mobile.png`。代码复审见 `code-review-2026-11-06-phase2.md`。
