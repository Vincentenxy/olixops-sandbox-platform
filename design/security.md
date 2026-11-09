# 身份、权限与状态边界

## 已实现的身份契约

参考项目采用 Bearer JWT、公钥验签、`staffuserid` 身份。服务端只允许 RS256/384/512；要求 exp 且用户 ID 非空；根据配置校验 iss/aud。前端仅保存当前标签页 sessionStorage 内的令牌；本地模式退出会删除数据库会话并清理前端令牌。用户资料接口不回传 JWT。

OIDC 授权码 + PKCE、JWKS 缓存轮换、BFF Cookie 会话、退出及刷新令牌是下一阶段；当前不声称已经接入 OIDC。

## 权限表

| 操作 | member | maintainer | owner | 配置中的平台 admin |
|---|---|---|---|---|
| 创建自己沙箱 | 是 | 是 | 是 | 是 |
| 查看/操作别人沙箱 | 否 | 仅同项目 | 仅同项目 | 是 |
| 查看成员 | 否 | 是 | 是 | 是 |
| 增加/更改 member/maintainer | 否 | 否 | 是 | 是 |
| 改变 owner | 否 | 否 | 否，本版不提供转让 | 否，本版不提供转让 |
| 查看审计 | 仅自己的动作 | 项目全部 | 项目全部 | 全部授权项目 |

身份匹配始终在后端执行。对无权访问的沙箱返回 404，不泄漏是否存在。命令、日志、文件、续期、删除和接口验证均走相同资源授权。命令和文件操作是对整个所属沙箱的授权，并非文件路径 ACL；沙箱内的 Linux 用户/网络隔离仍由运行时负责。

## 创建与配额

一个项目的创建预留持有 `projects` 行锁。先校验幂等键、统计非终态资源，再落库 Creating 和 requested 审计，然后发出远端请求。并发进程共享 PostgreSQL 锁，因此不依赖单机内存计数。

创建超时/5xx 保留 Unknown 及配额；后台按 operationId 标签查找实际资源。零匹配不是未创建的证明，不自动重放 create。后台已知远端 ID 的资源会轮询状态，Deleting 会重试删除；晚到的 Running 更新不能覆盖 Deleted/Deleting。远端 Failed 映射 Error，保留资源占用，便于手动清理；确认创建请求被 4xx 拒绝的本地 Failed 才释放预留。

当前 Unknown 无结果需要运维核查并处置，不提供普通用户强制释放按钮。该取舍优先避免漏记运行资源；后续增加管理员 Operation 恢复页及任务队列。每轮最多核对 200 条，通过 update_at 轮转；后续规模化改为分页队列与分布式租约。

## 访问与数据

- API 请求体最多 6 MiB；文件最多 4 MiB；前台命令最长 60 秒、返回流内容最多 1 MiB。
- 命令输出达到上限或 HTTP 断开不保证远端进程一定停止，后台命令应保留执行 ID，销毁时由运行时清理。
- 接口验证仅 GET、响应最多 2 MiB、禁止执行端口 44772 与 egress 18080，不跟随重定向，不渲染 HTML。
- 后端转发固定到 Server 的沙箱代理路径，用户不能传目标 hostname 或 backendId。沙箱内应用仍可主动访问网络，因此出口策略必须由运行时配套落实。
- 审计不保存命令文本、文件内容、凭据或预览响应，以免将秘密写入日志；记录 requested 与最终 success/failed。参数拒绝可能仅留下 requested。当前无不可篡改存储与留存归档。
- 操作审计预写失败则拒绝操作；远端动作后的审计写失败会记录服务错误日志，后续应补齐审计 outbox。

真实共享环境必须关闭员工直连共享 Server/execd/Gateway 的旁路，并确保 PostgreSQL、认证密钥、镜像白名单和 OpenSandbox runtime 安全策略按环境管理。

## 本地账号认证

详见 local-auth.md。密码使用 bcrypt cost=12，不保存明文。所有本地模式受保护接口在 JWT 验签后继续检查 jti 对应的 PostgreSQL 会话、有效期和用户 enabled 状态；没有会话的旧开发 JWT 不再有效。本地登录签发的 staffuserid 与原项目用户 ID 一致。

## 逻辑 Namespace 与持久工作区

每用户一个逻辑 Namespace，作为数据归属组织，不等于 Kubernetes Namespace，也不替代项目成员授权。Workspace 归属 project/owner；普通成员仅见自己的，项目管理者可管理同项目资源。对话与工作区绑定有同项目/同 owner 数据库外键和版本检查，外部对话 ID 不作为权限凭据。工作区 PVC 不由沙箱 ownerReference 管理，且不开放任意 claim/hostPath 输入。

第一阶段只支持存储创建和保留，不开放工作区/PVC 删除。创建沙箱事务预留工作区 active_sandbox_id；Unknown 保留独占。清理计算后还需 Server 对象消失、Pod/BatchSandbox 无消费者才解除占用。已保存的 PVC UID 不同或消失时阻止操作，不自动建空卷。目录文件管理仍通过沙箱授权，逻辑 Namespace 不构成单独的文件系统 ACL。
