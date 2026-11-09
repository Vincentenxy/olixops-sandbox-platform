# 本地 AI 关联与 MCP 认证核实

核实日期：2026-11-06；官方 `latest` 当前指向 **2026-07-28**。以下建议尚未实现。

- **HTTP MCP**：采用 OAuth 2.1（仍为 IETF 草案）、PKCE、受保护资源元数据及资源/audience 校验。令牌面向本 MCP 服务，失效返回 401。stdio 不套用远程 OAuth 流程，可从环境取得凭据。[授权规范](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization)、[安全要求](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization/security-considerations)。
- **公司登录**：OIDC 确认员工身份；OAuth access token 授权工具调用。ID token 不作为通用 API 凭据，接入 OIDC 仍需平台对象权限。[OIDC Core](https://openid.net/specs/openid-connect-core-1_0.html)。
- **关联由平台设计**：本地目录绑定 workspaceId；每轮执行取得 RunContext。MCP 不保证提供真实聊天 ID，也不自动上传代码。新版已取消协议级 session；旧版 session 和新版状态 handle 都不是权限凭据。[版本规则](https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning)、[工具](https://modelcontextprotocol.io/specification/2026-07-28/server/tools)、[状态句柄安全](https://modelcontextprotocol.io/docs/2026-07-28/tutorials/security/security_best_practices#state-handle-hijacking)。这是基于协议边界的设计结论。
- **本地文件**：Roots 在该版已弃用，且只作信息提示。文件选择、读取授权、同步与冲突策略须自行实现。[Roots](https://modelcontextprotocol.io/specification/2026-07-28/client/roots)。
- **建议首版**：受限、可撤销、过期的个人 PAT，加本地 stdio 适配器，调用平台 API。HTTP 静态令牌仅对支持自定义认证的客户端开放，不能声称完成通用 OAuth 兼容。正式方案采用远程 HTTP MCP 与 OAuth，接公司 OIDC。
- **权限边界**：取员工权限、授权资源、工具范围的交集，逐次检查。OpenSandbox 管理密钥仅留平台；客户端令牌不原样透传给 OpenSandbox。[令牌边界](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization/security-considerations#access-token-privilege-restriction)。
