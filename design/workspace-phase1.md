# 第一阶段：个人逻辑 Namespace、工作区与对话绑定

实现日期：2026-11-04。依据：用户已确认产品草案，并补充每用户逻辑 Namespace。

## 已实现的资源关系

`用户 → 个人逻辑 Namespace → Workspace → PVC / PV`；`外部对话引用 → Workspace`。

一个用户有一个稳定的个人空间，可以在不同项目创建多个工作区。工作区保留 projectId，项目成员关系和角色继续约束访问；普通成员看不到别人的数据，项目管理者可以管理同项目资源。逻辑 Namespace 不授予额外权限，也不对应 Kubernetes Namespace 或服务器目录。

个人空间第一次访问时自动登记。名字采用不可变随机 ID，不依赖账号名。工作区和对话记录通过 namespace_id/owner_id 复合外键绑定归属。对话永远不直接拥有 PV。

## 当前操作

1. 打开 http://127.0.0.1:5273，登录后选择项目，进入“持久化工作区”。
2. 点击“新建工作区”。系统使用受控模板在目标集群创建专属 PVC；WFFC Pending 是正常首次等待状态。
3. 点击“启动”，选择允许的镜像与 TTL，创建沙箱并挂载 `/workspace`。
4. 点击“查看实例”，使用现有文件和命令面板操作 `/workspace`；销毁实例后工作区保留。
5. 在“对话关联”登记客户端来源、稳定的外部对话 ID 和标题，选择同 owner 的工作区保存绑定。
6. 点击“定位数据”可查看原工作区、PVC、当前实例和绑定历史。隔天继续时先定位，再按需启动沙箱。
7. 空闲工作区可以归档或取消归档。归档保留数据，仍计配额。

第一阶段没有删除工作区/PVC 的公开入口。回收站、保留期清理、完整 Agent 执行调度/RunContext、自动继续对话启动实例、在线用量、聊天消息保存、备份恢复与存储迁移仍属后续阶段。没有因本阶段开发而启动自动删除策略。

绑定版本更新采用乐观锁；更换/解除绑定前须停止旧工作区运行实例，目标工作区也不能处于占用或归档状态。多个对话可以预先绑定同一空闲工作区，共用文件，不能把它理解为隔离分支。对话外部 ID 只在当前用户作用域内定位，不是访问凭证，也不代表已经取得外部 AI 平台的会话读取权限。

## 数据库脚本

PostgreSQL 脚本：`migrations/003_workspaces.sql`。包含 user_namespaces、workspaces、conversation_refs、conversation_binding_history，以及 sandboxes/audit_events 的兼容字段和索引/外键。

开发环境随 migrate=true 自动执行。已有库可由管理员按顺序执行：

```bash
psql "$DATABASE_DSN" -v ON_ERROR_STOP=1 -1 -f migrations/001_initial.sql
psql "$DATABASE_DSN" -v ON_ERROR_STOP=1 -1 -f migrations/002_local_auth.sql
psql "$DATABASE_DSN" -v ON_ERROR_STOP=1 -1 -f migrations/003_workspaces.sql
```

脚本可重复执行，不清空既有用户、项目、沙箱。生产由迁移任务执行后关闭自动迁移；不对未知 schema 直接应用。数据库文档和内嵌 workspace_schema.sql 同步维护。

## 存储配置与权限

workspace.enabled 控制新存储接入；禁用后保留数据库关系，但停止供应/核对。变更配置前应处理活动实例，避免失去核对能力。配置 cluster-id、Kubernetes namespace、storage-class 及大小/项目配额。已有工作区目标与配置不匹配时拒绝操作，不自动迁移。

本机显式使用 `/Users/vincent/.kube/config` 的 `poc-cluster`，目标 Kubernetes Namespace 为 `opensandbox`，StorageClass 为 `local-path`。这是开发环境配置；部署时将 kubeconfig/context 置空，使用专用 ServiceAccount，不能将本机管理员 kubeconfig 带入镜像。

生产最小权限示例见 deploy/workspace-rbac.yaml，包含目标 Namespace 的 PVC get/create、Pod/BatchSandbox list。第一阶段不需要 PVC/PV 删除或 Pod exec 权限。原生 RBAC 不按对象标签限制 PVC create，正式使用还应控制专属 Namespace、准入策略和 OpenSandbox 旁路。

默认申请 10Gi；每项目最多 5 个工作区、50Gi 申请容量。local-path 并非实际容量硬限额，用量未知时页面显示未知；节点数据需要备份。

## 失败和恢复

- Creating 记录是持久供应意图，后台每 15 秒尝试核对；PVC 创建冲突按稳定名字与平台标签核对。
- 一旦记录 PVC UID，丢失或同名换 UID 都不会自动创建新空卷；显示异常并等待运维核查。
- 创建沙箱事务同时锁定项目和工作区、预留计算配额和 active_sandbox_id；创建超时保持 Unknown，不重放创建。
- 已删除实例也必须确认 Server 资源消失、旧 Pod 和 BatchSandbox 不再引用 PVC 才释放占用；集群不可达时不解锁。
- 工作区 idle/archived 继续保留，无闲置删除。写在 `/workspace` 之外的系统修改及进程不属于持久化范围。

## 验证

全量 PostgreSQL + race 测试、Go vet/build、前端类型检查/构建已通过。针对性测试覆盖 Namespace 身份、工作区和对话越权、绑定版本、幂等、并发配额、独占、未退出消费者、Unknown 创建不重放，以及 PVC UID 漂移/丢失和工作负载消费者核查。

真实 PoC 验收：脚本 `python3 scripts/workspace-smoke.py --real` 创建两个顺序运行的沙箱，原 PVC UID 一致，文件 SHA256 一致；最后销毁运行实例并保留测试工作区/对话以便查看。

- 项目：持久化工作区验收。
- 工作区：长期代码验收，ID `00fc1777ab05a5c2811b909f7e3befad`。
- Namespace：`personal-1374f0403a96`。
- PVC UID：`9401565f-f8f1-45d5-8448-2348a23782da`。
- PV：`pvc-9401565f-f8f1-45d5-8448-2348a23782da`。
- 文件：`/workspace/persistence.txt`。
- 哈希：`d52ca5a6cf40854fc6fc9b5a47ef166f8c64ff21b860b644dbe56b2c48f4896c`。

脚本会产生真实资源，不作为每次启动前置步骤。不同时间重新运行会创建新的测试项目和工作区；不要无目的重复运行。

浏览器补充：已验证对话定位原 PVC、工作区归档/取消归档、从工作区打开挂载创建弹窗并取消；1440px 和 390px 下无页面横向溢出。验收完成后工作区处于未归档、无活动实例状态。
