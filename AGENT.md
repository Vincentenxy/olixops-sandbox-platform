# olixops-sandbox-platform 项目开发规范

> 本文件是本项目所有开发工作的**硬性规范**，任何代码变更（包括 AI 生成）必须严格遵循。后续开发前请先阅读本文件。

---

## 1. 接口规范

### 1.1 接口路径格式

所有对外接口统一格式：`/api/[版本号，如 v1]/[pub]/xxx/xxx/xxx`

- `/api/[版本号]/pub/...`：**无认证接口**，任何人可调用（如健康检查、分享链接）
- `/api/[版本号]/...`（不带 `pub` 前缀）：**认证接口**，必须携带合法凭证
- 路由分组与认证中间件必须以此前缀作为拦截依据

### 1.2 请求方法

只允许使用 **GET / POST** 两种方法：

- **带参数的接口**：统一使用 `POST`
- **不带参数的接口**，或用于分享的链接：使用 `GET`

### 1.3 响应数据格式

所有接口（包括错误响应）body 必须返回如下结构，字段名为小驼峰：

```json
{
  "code": 0,
  "msg": "",
  "data": {}
}
```

**code 状态码约定**：

| 范围 | 含义 |
|------|------|
| `0` | 成功 |
| `-1` | 通用失败 |
| `1 ~ 1000` | 系统预留失败状态码，其中 600 以内与 HTTP 状态码保持一致 |
| `10000 以上` | 业务失败状态码 |

### 1.4 错误处理（硬性要求）

- `pkg/response` 包提供快速生成响应的方法（Success / Fail 等），常规错误统一调用这些方法
- **所有错误响应（含 400 / 401 / 403 / 404 / 500 等）body 永远返回标准 response 结构体**，且 body 内 `code`、`msg` 与 HTTP 状态码、HTTP 状态文本对应，例如：

```
HTTP 状态码：401
HTTP body：
{
  "code": 401,
  "msg": "Unauthorized",
  "data": null
}
```

- 认证失败、参数校验失败等场景禁止直接返回纯文本或框架默认错误页，必须经过统一封装

### 1.5 接口文档

- 使用 **swaggo**（swag 注解）方式编写接口文档，注解写在 handler 上，生成文件输出到 `docs/`（该目录不提交版本库）
- 同时维护一份符合 **OpenAPI 协议**的独立文档，用于对外分发（生成后导出，方便提供给外部）

---

## 2. 认证规范

- 认证方式：请求头携带 `Authorization: Bearer <token>`
- Token 类型：**JWT**，使用**公钥验签**（仅允许 RS256/384/512，防止算法降级），公钥通过配置文件 `auth.jwt-public-key` 注入（兼容 base64 DER 与 PEM 两种格式）
- 认证实现：`middleware.Auth()` 在 router 层挂载，已实现：
  - 路径含 `/pub/` 前缀 → 跳过认证
  - 其余 `/api/` 路径 → 强制认证，失败统一返回 401 标准错误结构（见 1.4）
- **用户信息解析**（对应 Java UserContext 逻辑）：认证通过后写入 `pkg/userctx.User`：

| User 字段 | 来源 |
|-----------|------|
| `UserID` | claims.`staffuserid` |
| `Name` | claims.`name` |
| `Jwt` | 原始 token（下游透传用） |
| `Cookie` | 请求头 Cookie（下游透传用） |

- 业务代码获取用户：`userctx.MustFromContext(c)`，详见 README.md 认证章节

---

## 3. 数据库规范

- **PostgreSQL**：持久化存储
- **Redis**：缓存
- 所有数据库表设计必须记录在 `design/database.md` 中，保持与实现同步
- 文档格式：Markdown，表结构使用 **SQL 代码块**（```sql）包裹，附索引说明
- 所有数据表必须包含以下通用字段（数据库列名为下划线风格）：

| 列名 | 说明 |
|------|------|
| `create_at` | 创建时间 |
| `update_at` | 更新时间 |
| `create_by` | 创建人 |
| `update_by` | 更新人 |

---

## 4. 命名规范

- **Go 结构体字段 / JSON 字段**：小驼峰（如 `createAt`、`userName`）
- **数据库列名**：下划线（如 `create_at`、`user_name`）
- 其余命名遵循 Go 官方规范（包名小写、导出标识符大驼峰等）

---

## 5. 日志规范（硬性要求）

- **全局日志只允许使用 `pkg/logger` 模块**（zap 封装），禁止直接使用 `fmt.Println` / `log.Println` / zap 全局 Logger
- 日志格式：人类可读 + 级别带颜色（INFO 绿色系、ERROR 红色等），时间格式 `2006-01-02T15:04:05.000-07:00`
- **trace-id 透传**：HTTP 入口由 `middleware.RequestID()` 处理 `x-request-id` 请求头（携带则透传、缺失则生成、响应头回写）；日志中有 trace-id 时打印，没有则不打印
- 使用方式（详见 README.md 日志章节）：
  - HTTP 请求场景：`logger.Info(c, "msg", zap.Xxx(...))`，传入 gin.Context 自动携带 trace-id
  - 无 context 场景：`logger.L().Info("msg", ...)`
- 级别使用：Debug / Info / Warn / Error，生产环境（release 模式）输出 JSON 格式且仅 Info 及以上级别

---

## 6. 部署规范

- 项目最终部署在 **Kubernetes**
- 根目录维护 **Dockerfile**（多阶段构建，产出精简运行镜像）
- Dockerfile 需保证二进制可在无 Go 环境的容器中独立运行，配置文件以挂载或环境变量方式注入

---

## 附：当前项目结构基线

```
olixops-sandbox-platform/
├── cmd/main.go                          # 入口
├── configs/config.yaml                  # 配置
├── design/database.md                   # 表结构设计文档（SQL 代码块格式）
├── docs/                                # swaggo 生成文档（不提交版本库）
├── internal/
│   ├── domain/                          # 领域层
│   ├── application/                     # 应用层
│   ├── interfaces/                      # 接口层（handler / router / middleware）
│   └── infrastructure/                  # 基础设施层（config / db / redis / jwt）
├── pkg/
│   ├── logger/                          # 全局统一日志（zap 封装，禁止绕过）
│   ├── response/                        # 统一响应封装
│   └── userctx/                         # 认证用户上下文（JWT 解析结果存取）
└── Dockerfile                           # 多阶段构建，K8s 部署
```

## 附：已确认决策记录

- 表设计文档：Markdown + SQL 代码块格式（`design/database.md`）
- 接口文档：swaggo 注解生成 + 导出 OpenAPI 文档对外分发
- 业务错误码（10000+）：暂无分段规则，后续开发中逐步分段后补充到本文件


## 平台补充约定

- OIDC 尚未接入；当前使用参考工程的 RSA JWT 契约，必须校验 exp、staffuserid，以及配置的 issuer/audience。禁止开发认证绕过。
- 任意沙箱读写均先查数据库项目成员关系与 owner；不得信任请求的 role/owner/backendId。
- OpenSandbox API Key 只存在后端；预览不得将秘密或沙箱 HTML 放到平台同源页面。
- 数据库 SQL、design/database.md 与内嵌 schema.sql 必须一致。
- 对权限、配额、幂等和远端失败处理的修改需运行 PostgreSQL 集成测试。
- 前端构建及类型检查用 npm run build --prefix web。
