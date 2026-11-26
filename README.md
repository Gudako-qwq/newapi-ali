# newapi-ali · 渠道工作台

为中间商提供的**客户自助上号与日志工作台**。前端基于本地 New API 1.0.0-rc.50 的 React 组件、主题与渠道表单二次开发，后端仍是独立 Go 服务 + SQLite。生产二进制内嵌前端，不依赖 Node、Bun 或 Redis。参考项目的数据库、环境配置和用户凭据不会复制到本项目。

渠道页位于 `/channels`，默认卡片视图，可切换表格、按名称/本地 ID/模型/模板/状态筛选、排序和分页。抽屉只保留基本信息、凭证、模型与分组；没有开放原生平台的敏感设置。原生代码沿用其版权声明；前端许可证、NOTICE、第三方清单位于 `web/public/`，构建后可通过 `/LICENSE`、`/NOTICE` 和 `/THIRD-PARTY-LICENSES.md` 访问。

## 已实现的范围

- 本地账号登录、退出、修改密码；管理员开通/停用客户，停用立即撤销登录。
- 创建渠道、查询本地列表、手动同步状态、启用/停用；默认支持已在目标站观察到的 Vertex AI 服务账号 JSON 与 API Key 两种形式。
- 本地失败记录归档：客户可在自己的「被拒绝」记录的 `⋯` 菜单选择「归档失败记录」，管理员可对所有客户执行同样操作。确认后记录从默认列表移走，在「状态 → 已归档」只读查看。已有审计和提交标识保留；不删除、不重试、不调用上游。待确认、提交中及已绑定的渠道不能归档。
- 每次创建先写入持久化归属和幂等记录，再提交上游；结果不明时保留“待确认”，不自动重放创建请求。
- 仅按明确的、已归属的渠道查询调用日志；时间范围 1–30 天，50 条/页。
- 管理员核验连接权限、将上游已有渠道绑定给指定客户、查看最近 100 条本地操作审计。
- 客户看不到上游域名、登录账号、会话、渠道 ID、分组、代理、计费、其他客户、完整渠道配置及原始错误内容。

**不包含**：模型请求 `/v1/*` 转发、充值、计费、令牌管理、模型市场、OAuth 上号、批量上号、删除渠道、原生全功能后台。这里转发的是渠道管理与日志请求，不是另建一个完整模型网关。初版不提供注册入口和自动密码找回。

## 本地启动（Windows / Go 1.26+）

```powershell
Set-Location E:\workspace\project-1\newapi-ali
if (-not (Test-Path .env)) { Copy-Item .env.example .env }
notepad .env
.\start.ps1
```

在 `.env` 中填写：

| 配置 | 用途 |
| --- | --- |
| `BOOTSTRAP_ADMIN_USERNAME` | 首次启动的本地管理员，默认 `admin` |
| `BOOTSTRAP_ADMIN_PASSWORD` | 首次启动必填，正式部署须为 15–128 字符；与上游密码分开 |
| `LOCAL_ALLOW_WEAK_BOOTSTRAP_PASSWORD` | 默认 false；仅限回环 HTTP 开发模式的首次管理员，可使用 8–128 字符密码；Docker 强制关闭 |
| `UPSTREAM_USERNAME` / `UPSTREAM_PASSWORD` | 你们公司的上游账号，仅服务端读取 |
| `PUBLIC_ORIGIN` | 浏览器实际访问的来源，默认 `http://127.0.0.1:3000`，不能混用 localhost |
| `LISTEN_ADDR` | 默认仅监听本机；Docker 容器内监听 `0.0.0.0:3000` |
| `ALLOW_PUBLIC_HTTP` | 默认 `false`，仅在明确接受明文访问时设为 `true`；不会放宽来源、CSRF 或上游 HTTPS 校验 |
| `PUBLISH_ADDR` | 仅用于 Compose，默认 `127.0.0.1:3000`；直接对外提供服务时使用 `0.0.0.0:宿主机端口` |
| `TEMPLATES_FILE` | 可选，自定义模板 JSON 路径 |

访问 `http://127.0.0.1:3000`，登录本地管理员，先在“客户管理”中“检查连接”，再开通客户。上游凭据可以暂不配置，此时本地管理仍可用，但上号和同步会明确失败。

`.env` **不会被 Go 自动加载**：Windows 使用 `start.ps1`；直接运行二进制时由服务管理器注入环境变量。首次初始化后可移除 `BOOTSTRAP_ADMIN_PASSWORD`；修改它不会重置已有账号密码。不要删除数据库来重置密码，否则会丢失归属映射。

后台启动使用 `.\start.ps1 -Background`，停止使用 `.\stop.ps1`。后台进程信息和日志位于 `.local/`，只用于本地运维。上述启动脚本优先使用 `bin/newapi-ali.exe`；修改源码后应先重新构建再启动。

如果显式启用本地短密码例外，服务只能使用回环 HTTP 地址及回环监听。本地短密码管理员会被标记为“仅限开发”，**即使复制数据库到服务器也会阻止生产启动**。迁移前先在本地“修改密码”中设置至少 15 字符的新密码，再移除本地例外开关。例外不适用于新增客户或修改密码，不会降低这些操作的要求。

构建与验证：

```powershell
Push-Location web
bun install --frozen-lockfile
bun run build
bun run test
Pop-Location
go test -count=1 ./...
go vet ./...
go build -trimpath -o bin/newapi-ali.exe .
```

从源码测试或构建 Go 程序前必须先生成 `web/dist`；Docker 会在独立 Bun 阶段完成这一步。默认 Go 测试使用模拟上游和临时 SQLite，不访问真实号池。

## 模板与已有渠道

复制 `templates.example.json` 为 `templates.json`，设置 `TEMPLATES_FILE=templates.json`。模板决定 `type`、`group`、区域、`base_url`、`setting`、`settings`。创建 Vertex AI 渠道时，模型 ID 由客户手动填写，不由模板预选，也不受模板示例模型列表限制。管理员绑定已有渠道及其他类型模板的逻辑不变。

- Vertex JSON：必须是单个 Google `service_account` 对象，token 地址固定为 Google 官方地址；不接受数组或外部身份配置。
- 自助创建表单的「类型」仅显示 Vertex AI。凭证区另选 JSON 或 API 密钥，两者使用各自的服务端模板；切换格式会清空旧凭据，但保留名称和手填模型。API 密钥模式可填写项目 ID；部署区域必填，预填模板默认值，可输入一个允许区域或 `{"default":"us-central1","手填模型":"global"}` 形式的映射。基础地址、分组与代理不向下游开放编辑。
- 模型栏初始为空，无下拉候选和快捷填充。每行一个 ID，也可用中英文逗号分隔；页面去除首尾空白、跳过空项、按输入顺序去重并保留大小写。每个渠道需 1–100 个模型，每项最多 256 字符。填写模型并不代表该凭据有调用权限，请填写账号实际支持的模型。
- 其他接受单个 API Key 的 New API 渠道类型可添加服务端模板，但**未逐个验证其他提供商**；需要特殊认证的类型应先补充适配器与测试，不应直接放行任意 JSON。
- 不复制现有渠道的代理凭据。若业务需要固定代理，应由管理员在私有模板的 `setting.proxy` 中配置。
- 新建渠道直接发送客户填写的名称（只去除首尾空白，无前后缀）；允许重名。仅用上游创建回执的单一 ID 核实名称、类型并绑定；同一上游 ID 仅能归属一条本地记录。历史的 `ali_…` 名称不会自动更改，管理员或渠道所有者可通过「⋯ → 编辑渠道」主动保存原名。名称、模型以外的编辑字段由服务器保留，不提供修改密钥、代理或分组的接口。
- 已有号池不会自动展示给客户。“绑定已有渠道”仅管理员可用，输入渠道 ID、客户、类型和本地展示名称，核验后建立唯一映射；这是授予客户读取日志/管理该渠道的权限，请先核实归属。
- 列表显示最近同步值。对未取得有效上游 ID 的待确认记录，请管理员在上游逐一核实客户归属后，使用原记录的「⋯ → 人工核对并绑定」填写 ID，不要另建或重复上号。普通客户不能认领同名渠道。

## 目标站接口核对

观察日期：**2026-11-26（Asia/Shanghai）**。通过公开页面资源及授权账号的只读请求核对；没有保存完整上游响应或抓取到的私密配置。

| 上游接口 | 作用 | 核对情况 |
| --- | --- | --- |
| `GET /api/status` | 登录策略 | 实测；当时 `password_transport.mode=disabled`，未启用 Turnstile |
| `POST /api/user/login` | 服务端登录 | 实测；返回 `data.user` / `data.session`，设置 `new_api_session` |
| `GET /api/user/self` | 权限能力 | 实测；授权账号具备渠道读写及 `usage.read` |
| `GET /api/channel?p=1&page_size=1` | 渠道列表 | 实测；`data.items/total/page/page_size` |
| `GET /api/channel/search?keyword=...` | 只读协议检查 | 不用于建立客户归属 |
| `GET /api/channel/{id}` | 渠道详情/状态 | 实测；读取后仅保留安全字段 |
| `POST /api/channel` | 创建渠道 | 真实供应商账号已验证 Vertex API Key 单密钥创建、名称核对和本地绑定；上游成功归档测试渠道 |
| `PUT /api/channel/` | 编辑渠道名称、模型 | 真实隔离测试渠道完成同一 ID 改名和模型更新，读回确认后归档；保留末尾斜杠 |
| `POST /api/channel/{id}/lifecycle` | 启停 | 前端资源确认 `{"action":"enable"或"disable"}`；真实写入尚未验证 |
| `GET /api/supplier/logs` | 客户号池调用日志 | 实测；`channel_id` 过滤有效，分页使用 `p/page_size` |
| `GET /api/log/` | 原生管理日志 | 授权账号返回 403；本项目不使用 |
| `GET /api/channel/ops` | 管理能力接口 | 授权账号返回 403；本项目不使用 |

重要区别：目标站是包含组织/供应商角色的定制版，不是原生 New API 的简单换皮。日志必须使用供应商接口，不能照搬原生管理员 `/api/log/`。

供应商账号创建 Vertex 渠道的请求体除 `mode` 和 `channel` 外，还需要顶层 `security_gateway` 与 `request_id`。旧报文实测被上游以 `HTTP 409 / SECURITY_GATEWAY_REQUIRED` 拒绝。适配器使用上游页面的默认随机网关策略和每次创建独立的 UUID，不接收客户端提供的代理或网关参数。`channel.other` 在单区域模式下为普通字符串，按模型配置区域时才为 JSON 字符串。管理员可在审计日志中查看新拒绝的 HTTP 状态与已知错误类别，不记录 Cookie、密钥或原始错误正文。旧拒绝记录不会自动重放。

创建只接受本次回执中唯一且一致的 `data.id`、`data.ids` 或 `data.results[].channel_id`，再按 ID 读取详情校验。没有可靠 ID、超时、5xx、响应不明确或进程中断时保留待核对状态，不根据名称猜测归属。升级时事务迁移原名称唯一约束，保留本地 ID、上游绑定、幂等键与审计；上线前请正常备份数据库。

当前适配器要求已核实的 Cookie 登录协议；若上游将来启用密码传输加密、验证码、MFA，或更换认证协议，会停止连接并返回固定错误，而不是尝试绕过验证。上游写请求不自动重试；GET 在 401 时允许重新登录一次。

## 本地接口边界

所有 `/api` 响应采用 `success/data/message`。写操作要求正确 `Origin`、`application/json`；登录以外还要求会话和 `X-CSRF-Token`。从 `GET /api/session` 获取 CSRF Token。页面通过同源 Cookie 使用服务，不需要 API Key。

| 接口 | 权限 / 内容 |
| --- | --- |
| `POST /api/login` | 本地账号密码 |
| `GET /api/session`、`POST /api/logout`、`POST /api/password` | 当前本地账号 |
| `GET /api/templates` | 脱敏模板，只含客户所需字段 |
| `GET /api/channels?page=1` | 本人渠道；管理员可查看所有本地绑定 |
| `POST /api/channels` | `name/template_id/credential/models/region/project_id`；必须提供 16–64 字符 `Idempotency-Key` |
| `POST /api/channels/{本地ID}/sync` | 先校验归属，再查询上游 |
| `PUT /api/channels/{本地ID}` | 本人或管理员编辑已绑定渠道，仅接受 `name`、`models`，上游读回一致后才更新本地 |
| `POST /api/admin/channels/{本地ID}/reconcile` | 管理员显式核对待确认记录，接受 `upstream_id`，更新原有记录而非新建 |
| `POST /api/channels/{本地ID}/archive` | 请求体 `{}`，仅本地归档明确被拒绝且未绑定的记录；重复操作幂等。`GET /api/channels?status=archived` 查询归档记录 |
| `POST /api/channels/{本地ID}/lifecycle` | 只允许 `enable/disable` |
| `GET /api/channels/{本地ID}/logs?page=1&days=7` | 服务端强制注入上游渠道 ID；禁止透传查询条件 |
| `GET/POST /api/admin/users`、`PATCH /api/admin/users/{id}` | 管理客户账号；不开放任意角色修改 |
| `POST /api/admin/bind` | 管理员显式绑定已有上游渠道 |
| `GET /api/admin/connection`、`GET /api/admin/audit` | 管理员专用 |

没有 `/api/proxy/*` 或任意上游路径透传。即使绕开 UI 直接调用接口，也无法查询没有本地归属的渠道或整体号池日志。若上游错误返回其他渠道的日志，本项目会拒绝整个响应，而不是把它展示出来。

## 安全与生产部署

- 密码使用 Argon2id（19 MiB、2 轮、1 并行度）；会话随机 256 bit，数据库仅存 Cookie 的 SHA-256 摘要，12 小时固定有效期。
- Cookie 为 HttpOnly / SameSite=Strict，HTTPS 下启用 Secure；写请求校验 Origin 与同步 CSRF Token。修改密码、退出和停用账号会撤销对应会话。
- 禁止客户端选择上游地址、代理、任意 Header、owner、渠道 ID、分组、优先级；请求体严格拒绝未知字段。
- 提交凭据不写入数据库或审计；数据库只保存归属、名称、模型、状态、幂等内容摘要等。上游密码从服务端环境读取，Cookie 只在进程内存中。
- 不转发原始错误、Header、Cookie、日志计费信息、请求内容或供应商身份；页面没有第三方脚本/分析追踪。
- 限制请求体、响应体、登录频率、单用户请求频率和并发上游请求；不信任客户端 `X-Forwarded-For`。部署反向代理后建议额外做边缘限流，应用看到的是代理 IP，共享登录 IP 额度可能影响多人同时登录。
- 本项目仅实现单实例 SQLite。不要多副本共享此数据库。定期备份完整 SQLite 状态，停机备份，或使用 SQLite 在线备份；WAL 模式下不能只拷贝主文件。
- `.env`、私有模板和数据库必须限制操作系统读取权限并排除代码提交、公开备份与静态目录。Windows 文件 ACL 应由部署人员设置，不能仅依赖 Unix 权限位。
- 推荐配置 HTTPS 反向代理，TLS 终止后将请求转到本地监听地址；保留 Origin 和 Cookie。如果需要直接以公网 IP 通过 HTTP 访问，请使用下面的显式部署配置，不要复用本地短密码模式。
- 管理账号暂不支持本地 MFA、密码找回或在线密码重置；建议管理员入口增加 VPN/访问控制。生产前需根据你们的威胁模型补充运维安全审查，不能将当前测试等同于完整 ASVS 认证。

### Docker 服务器部署

本地 `.env` 与生产 `.env.production` 分开管理，二者都不进入 Git 或镜像。首次部署从 `.env.production.example` 复制配置；已有 `.env.production` 请直接编辑，不要覆盖凭据。填写访问地址、至少 15 字符的初始管理员密码和上游凭据。

**公网 IP + HTTP（无域名或证书）**：例如浏览器要访问 `http://203.0.113.10:3000`，在 `.env.production` 中设置以下三行；把示例 IP 替换为自己的公网 IP。`0.0.0.0` 只用作监听地址，不能写进 `PUBLIC_ORIGIN`。若更换端口，两个地址的端口都要改。

```dotenv
PUBLIC_ORIGIN=http://203.0.113.10:3000
ALLOW_PUBLIC_HTTP=true
PUBLISH_ADDR=0.0.0.0:3000
```

在云安全组和主机防火墙中允许需要的来源访问该 TCP 端口；能限制客户 IP 时优先限制。此模式下浏览器与工作台间的登录密码、会话 Cookie、提交的 API Key / 服务账号私钥均无传输加密，可能被窃听或篡改。来源校验和 CSRF Token 不能代替 TLS。服务端连接上游仍强制 HTTPS。

**HTTPS 反向代理（默认）**：使用 `PUBLIC_ORIGIN=https://你的域名`，保留 `ALLOW_PUBLIC_HTTP=false` 和 `PUBLISH_ADDR=127.0.0.1:3000`。两种模式均使用以下命令部署或更新；在服务器取得新版代码后，修改配置并重新构建，不是只重启旧容器：

```sh
chmod 600 .env.production
docker compose --env-file .env.production up -d --build
docker compose --env-file .env.production logs --tail=100 ali
```

默认只发布到宿主机回环地址；在两种模式下 `PUBLIC_ORIGIN` 都必须与浏览器实际使用的协议、主机和端口一致，不带路径。容器使用非 root 账户、只读根文件系统和独立 `ali-data` 持久卷，强制禁用本地短密码例外。初次在服务器部署会初始化独立数据库，不会自动带入本地数据库或管理员密码。迁移现有数据库时先完成密码升级，再使用停机备份或 SQLite 在线备份，并确保容器用户 UID 65532 可写数据目录。

自定义模板需要额外添加只读 volume 与容器内 `TEMPLATES_FILE`。此环境没有 Docker，镜像构建与容器运行需要在服务器上再验证。不要直接将本地开发用 `.env` 上传到服务器。

安全设计参考 OWASP Authentication、Session Management、Password Storage、CSRF Prevention Cheat Sheets；这些是设计参考，不是对全部条款合规的承诺。

## 真实上游测试边界

可选只读协议检查（需要在进程环境中提供上游凭据）：

```powershell
$env:LIVE_UPSTREAM_CHECK='1'
go test -run TestLiveReadOnlyProtocol -v
Remove-Item Env:LIVE_UPSTREAM_CHECK
```

该检查只登录、读取本人可见的渠道与单渠道日志，并退出其新建的上游会话；不会创建、启停或删除渠道。自动化回归另外覆盖创建、日志隔离、重放、权限、CSRF、重定向、会话过期和撤销等场景，使用模拟上游。

本次在浏览器中对照了授权上游的渠道页及创建抽屉，并验证了本地新版渠道页的视图切换、打开抽屉、切换 Vertex 凭据类型、取消以及调用日志导航。本地验收时没有已绑定渠道，没有为了截图而导入生产数据。前端测试覆盖回跳路径、状态显示、会话隔离与提交请求；Go 回归覆盖接口、筛选与客户归属。构建产物位于 `bin/`，不要把测试通过理解为真实上号验收已经完成。

**真实写入与推理验收分开**：经授权在隔离的本地实例通过实际适配器向真实号池提交了一条假密钥的 Vertex API Key 测试渠道，确认本地 `201 / ready`，随后归档并核实已不在上游活动列表。该测试只证明创建和绑定成功，不证明凭据或模型可调用。服务账号 JSON、真实推理、启停与调用日志的完整业务验收仍需有效的授权测试凭据。
