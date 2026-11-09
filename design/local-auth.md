# 本地账号密码认证

## 使用

运行 `./scripts/dev.sh` 后打开 http://127.0.0.1:5273，账号 `vincent`。初始随机密码保存在本机 `.local/local-password.txt`，可以 `pbcopy < .local/local-password.txt` 复制。密码、RSA 私钥均在 Git/Docker 忽略目录中。

第一次启动先迁移用户和会话表，再创建引导账号。已有账号不会因环境变量或密码文件改变而被重置。此文件只是首次引导凭据的本机副本，不是后续用户密码的配置源。

## 登录契约

- `POST /api/v1/pub/auth/login`：`{"username":"vincent","password":"实际密码"}`。
- 成功返回 `{code:0,msg:"success",data:{accessToken,tokenType:"Bearer",expiresAt}}`。
- 请求继续使用 `Authorization: Bearer <accessToken>`；默认 8 小时过期，不自动刷新。
- `POST /api/v1/auth/logout`：撤销当前 jti；原令牌立即失效，不会撤销该账号其他设备的会话。
- 错误账号、错误密码和停用账号统一 401；同一来源 IP 一分钟超过 10 次登录请求返回 429。限流为进程内控制，集群部署时应迁移到网关或共享存储。
- 不开放公共注册。项目角色仍来自项目成员表，不来自登录请求。

密码使用 bcrypt cost=12，长度约束为 8～72 字节。账号允许字母、数字、下划线、点、连字符，最长 64 字符。显示名与用于权限关联的 user_id 分开。

## 创建其他本地账号

在项目根目录执行，密码通过环境变量传入，避免出现在命令参数中：

```bash
read -s LOCAL_USER_PASSWORD
export LOCAL_USER_PASSWORD
env -u GOROOT go run ./cmd/user --username alice --name Alice
unset LOCAL_USER_PASSWORD
```

CLI 为幂等创建，已有账号不修改原密码；可在项目成员页把 `alice` 添加为 member/maintainer。命令要求本地签名私钥和数据库配置已可用。暂不提供用户自助注册、找回密码、修改密码或管理用户页面。

## 配置

```yaml
auth:
  issuer: olixops-local
  audience: olixops-agent-platform # 保留已有认证标识，与项目展示名称独立
  local:
    enabled: true
    private-key-file: .local/jwt-private.pem
    ttl-seconds: 28800
    bootstrap-user: vincent
    bootstrap-password: "" # 首次引导通过 AUTH_LOCAL_BOOTSTRAP_PASSWORD 注入
```

开发脚本会加载本机密码文件；容器部署时必须挂载持久 RSA 私钥，注入首次引导密码，并保持多个后端副本使用相同签名密钥。不能将 .local 目录打进镜像。默认配置不会在生产自动生成私钥。

设置 `auth.local.enabled=false` 后，回到原外部 JWT 公钥验证模式：此时本地登录/退出端点不注册，必须配套外部登录入口。当前前端专门提供本地账号表单；公司 OIDC 前端流程仍待接入。

## 数据与退出语义

local_users 保存哈希与停用状态；local_sessions 保存随机 jti、用户 ID、有效期。每次请求查数据库，因此退出、停用和过期可立即阻断后续请求。已在执行中的远程命令不因退出自动终止。

会话及签名密钥保持不变时，重启服务不影响有效会话。初始 `vincent` 的 user_id 继续为 vincent，因此已有项目、成员关系和审计无需迁移。旧开发脚本生成但未登记会话的 JWT 在本地模式下会被拒绝。
