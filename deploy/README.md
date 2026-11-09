# Kubernetes 部署准备

本轮不执行发布。先构建后端 Dockerfile 与 web/Dockerfile。web Nginx 通过集群 Service `olixops-agent-platform:18090` 转发 `/api/`。前端使用根路径，不复用 OpenSandbox `/osb` 前缀。

后端 Deployment 挂载 configs/config.yaml 或通过环境变量覆盖；`SERVER_HOST=0.0.0.0`。公司 JWT 公钥、Server API Key 与数据库连接应按公司配置/Secret 方案注入，不写入镜像。 readiness/liveness 可使用 `/api/v1/pub/health`（当前仅进程存活检查，不表示 OpenSandbox 可用）。

数据库初始化 migrate=true 仅适合初始开发；正式部署由迁移任务应用 migrations/001_initial.sql 、migrations/002_local_auth.sql 和 migrations/003_workspaces.sql 后关闭自动迁移。后续扩容需监控后台核对的重复请求，生产推荐独立 worker 或任务租约。

入口通过 Higress/现有 Ingress 到前端 Service，前端再同源转发 API。后端现在会创建和读取工作区 PVC，并核对 Pod/BatchSandbox 消费者。配置 workspace.kubeconfig/context 为空以使用专用 ServiceAccount，最小权限模板见 workspace-rbac.yaml；禁止挂载集群管理员 kubeconfig。第一阶段不提供 PVC 删除，不需要 PV 写权限。OIDC、用户应用独立域预览、生产备份与集群网络准入仍需在多人正式使用前落实。

本地账号模式需要挂载 auth.local.private-key-file 所指向的持久 RSA 私钥，并在首次引导时注入 AUTH_LOCAL_BOOTSTRAP_PASSWORD。不要在各副本启动时重新生成随机签名密钥。
