# 遗留问题修复方案（v2，已按 Codex 评审修正）

日期：2026-10-08。范围：上一轮评审后仍未解决的 5 件事。产品名 SupaCove，技术标识仍为 `supabackup`。

## 不在本轮范围

- 首页标题轮播里的未实测平台（用户指定的设计，保留）。
- 后端技术标识改名、`CHANGELOG.md`、历史评审记录。
- 给控制台新增“存储目的地”配置界面（产品功能，不是文档修复）。
- 修改后端行为。实测中发现的后端问题只记录、上报，不在本轮改。

## 执行顺序

1. **F5 冒烟脚本先行**，对当前构建跑出基线。
2. **F4** 构建警告 → **F3** 404 语言，每步后重跑冒烟。
3. **F1** 目的地文档（起本地环境实测）。
4. **F2** Supabase 指南实测（复用 F1 的环境）。
5. 全量构建、冒烟、截图检查；对最终改动跑一次 Codex 评审并修复；更新 `docs/seo-plan.md`。不提交，除非用户要求。

## F5 · 冒烟脚本（防 `globalNotFound` 等回归）

新增 `docs-site/scripts/smoke.mjs`：对已构建的站点起 `next start`（空闲端口、有上限的就绪轮询、退出时必定杀掉子进程），手动处理重定向，断言：

- `/`、`/zh`、`/docs`、`/zh/docs`、一篇指南：200。
- `/en`、`/en/docs`：308，`Location` 为无前缀地址，并下发 `sc_lang=en`。
- `/nope`、`/zh/nope`、`/docs/nope`、`/zh/docs/nope`：404，原始 HTML 含自定义 404 的 `<h1>` 文案与 `noindex`。
- 中文 `Accept-Language` 访问 `/?utm=x`：307 到 `/zh?utm=x`，带 `Vary`；带 `sc_lang=en` cookie 时为 200；`zh;q=0` 为 200。
- `/sitemap.xml`、`/robots.txt`、`/opengraph-image`：200，后者为 PNG。
- 首页与文档页的 `og:image`、`twitter:image` 是 `https://supacove.com/…` 绝对地址。

CI 的 docs 任务在 build 之后运行它。`docs-site/README.md` 写明 404 依赖实验性开关、由该脚本看守。

## F4 · 构建警告 “metadataBase is not set”

**原因（评审给出，需先验证）**：`app/opengraph-image.tsx` 属于没有布局的根段，`metadataBase` 只在子级的语言布局里设置。Next 先解析父级的文件图片，此时没有 `metadataBase`，于是告警；随后页面级元数据覆盖了结果，所以最终 HTML 是干净的。

**做法**：把同一份渲染代码改为路由处理器 `app/opengraph-image/route.tsx`（导出 `GET`，保持静态缓存），地址仍是 `/opengraph-image`，`OG_IMAGE.url` 不变。不为消除警告而加共享根布局。若验证后原因不符，按实际定位处理；定位不到则在 README 记录为已知无害警告。

**验证**：警告消失；冒烟脚本里的 OG/Twitter 断言与图片抓取通过；404 页面的 `og:image` 另行确认（它原先依赖文件约定，改后需显式声明或接受没有分享图）。

## F3 · 404 页面按语言显示

静态页里同时输出英文和中文两个完整版本（各带 `lang`）。内联脚本在首次绘制前判断 `pathname === "/zh" || pathname.startsWith("/zh/")`，给 `<html>` 设置 `lang` 与标记，CSS 隐藏另一种语言，并同步 `document.title`。脚本被禁用时两种语言都显示。

如实描述：这是“静态双语 HTML + 客户端选择语言”，不是服务端本地化。

**验证**（浏览器，不只是原始 HTML）：`/nope` 英文、`/zh/nope` 中文、`/zhish` 英文；返回链接指向各自语言；禁用脚本时两段都可见；状态码与 `noindex` 不变。

## F1 · “存储目的地”文档页（中英）

新增 `content/docs/{en,zh}/destinations.mdx`，顶层导航放在 `databases` 之后。开头如实说明：目前只能通过 API 配置，没有控制台界面、命令行或环境变量方式；绑定之后，手动备份和定时备份会自动使用它。

**内容**

1. **认证片段（可复用）**：`POST /api/auth/login`（JSON 用户名密码，`curl -c cookies.txt`）；从 cookie 文件取 CSRF 值；之后每个 POST/PUT/DELETE 带 `-b cookies.txt -H "X-CSRF-Token: …"`，GET 只需会话。说明生产环境 cookie 名是 `__Host-sb_session` / `__Host-sb_csrf`，开发模式（`SB_INSECURE_COOKIE=1`）是 `sb_session` / `sb_csrf`；curl 不要自己加 `Origin`，加了就必须与 `SB_PUBLIC_ORIGIN` 或请求的主机一致。
2. **创建**：三种平台的字段规则（r2/b2 必须给 endpoint；b2 必须给 region；s3 默认 `us-east-1`；r2 默认 `auto`；bucket、prefix 字符规则）。创建前做真实连通性测试，失败不落库。bucket 必须事先存在。示例里**显式传 `verifyReadback`**。
3. **绑定、测试、对账**：`PUT /api/databases/{id}/destination`、`POST …/test`、`POST …/reconcile`（对账只比较、不修复）。
4. **没有更新接口**：改配置 = 新建一个再改绑。
5. **删除（单独一节，放在最后）**：先用 `{"destinationId": null}` 解绑再删。删除是软删除，不清理远端对象；不解绑就删，之后的备份会失败；历史备份的远端下载可能不再能通过应用获取。有上传在进行时拒绝。
6. **凭据保存**：只有 `secretKey` 用主密钥加密，`accessKey` 明文存储；两者都不会出现在 API 响应里。主密钥变更后需重建目的地。
7. **保留策略**：`keepRemote` / `keepDays`，链接到 `scheduling`。
8. **错误码**：`name_exists`、`diagnostic_test_failed`、`upload_in_flight`、`not_assignable`、`csrf`。

**实测环境**：不直接用仓库的 `compose.yaml`（端口固定，会与本机已有容器冲突）。在临时目录用独立的 compose 文件：应用（本地构建）+ MinIO + PostgreSQL，全部绑回环上的空闲端口；用 `mc` 预先建 bucket；等就绪后 bootstrap 管理员、初始化 age 接收方；目的地 endpoint 用应用可达的 `http://minio:9000`。页面上每条 curl 原样跑一遍，并完成一次真实备份 + 远端下载。预签名下载地址用的是容器内主机名，从宿主机测试时要考虑这一点。

**如实边界**：MinIO 只验证 S3 兼容的机制，不代表 R2 / B2 实测过，页面上写明。跑不通的步骤不是删掉，而是记录失败、修正流程或写成明确的限制。

**需上报而不修的后端问题**：OpenAPI 声明 `verifyReadback` 默认 `true`，但处理器在省略时按 `false` 处理（`api/openapi.yaml:1338` 对 `api_phase3.go:78`）。实测确认后写进最终报告。

**同步**：`check-parity.mjs` 通过；其他页面提到目的地处加链接。

## F2 · Supabase 指南实测

**环境**：`supabase/postgres:15.8.1.085` 作为**源库**；目标是指南要求的**空的普通 PostgreSQL**（不是另一个 Supabase 实例，否则预装的角色和扩展会掩盖准备步骤的缺漏）。

**步骤**

1. 清点源库实际有哪些 schema、扩展、角色，不假设镜像等同于完整的 Supabase 项目。
2. 灌入有代表性的数据：`public` 下的表、行级安全策略、对 `anon` / `authenticated` 的授权、至少一个扩展类型的列。
3. 按指南逐步执行：注册 → 备份 → 下载恢复套件 → 按 manifest 准备目标库的角色和扩展 → 恢复。
4. 验证恢复结果：行数与内容、授权、策略、扩展，而不只是脚本退出码。
5. 把指南里与实际不符的地方改掉；目标库需要的扩展包如果普通镜像里没有，写明用什么镜像或怎么装。

**做不了的**：托管平台特有行为。指南里区分三种连接方式——直连 5432、会话模式连接池 5432、事务模式连接池 6543——并说明哪种适合 `pg_dump`。

**页面措辞**：成功则改为“已在 `supabase/postgres` 镜像上走通备份与恢复；尚未在 Supabase 托管项目上验证”，并列出未验证项。失败则保留原提示，把失败原因写进“常见问题”。

## 风险

- F1 的流程步骤偏长；如实记录，并在报告里向用户提出“是否要做控制台界面”。
- F2 结论只能写到镜像这一层。
- 本机测试用独立 compose 项目和空闲回环端口，结束后清理，不触碰本机已有容器。

---

## 执行结果（2026-10-08）

| 项 | 结果 |
|---|---|
| F5 冒烟脚本 | `docs-site/scripts/smoke.mjs`，52 项断言通过，已接入 CI 的 docs 任务 |
| F4 构建警告 | 原因如评审所述；分享图改为路由处理器 `app/opengraph-image/route.tsx` 后警告消失，地址不变 |
| F3 404 语言 | 静态双语 HTML，`/zh` 下由内联脚本切到中文；`/nope`、`/zh/nope`、`/zhish` 截图核对过 |
| F1 目的地文档 | `destinations.mdx` 中英两版；页面上的 curl 在本地实例 + MinIO 上全部执行过 |
| F2 Supabase 指南 | 在 `supabase/postgres:15.8.1.085` 上实测；“恢复”一节按实际结果重写 |

### 实测中发现、本轮未改的后端问题

1. **恢复套件无法完成 Supabase 备份的恢复。** 套件固定使用 `pg_restore --exit-on-error`：普通 PostgreSQL 上停在 `CREATE EXTENSION pg_graphql`；Supabase 镜像上以 `postgres` 身份停在 `vault.secrets` 的权限错误；以超级用户身份停在 `graphql_public.graphql` 的授权。去掉 `--exit-on-error` 后以 1 个错误完成，数据、策略、授权核对无误。对应 `docs/adr/ADR-003`，该 ADR 仍是“待执行”。
2. `verifyReadback`：接口文档写默认 `true`，省略时实际保存 `false`；且该字段目前不影响任何行为（上传后的读回校验始终执行）。
3. 删除仍被绑定的目的地会成功，之后该库每次备份都失败（`resolve destination: destination not found`，错误类别为 `unknown`）。
4. 目的地删除后，`GET /tasks/{id}/download-url` 返回 500 且响应体的 `code`、`message` 为空。
5. `DiagnosticTest` 里测试对象删除失败的错误被丢弃（`defer` 改的是局部变量，函数返回的是未命名返回值），创建仍返回 201。
6. `POST /destinations` 的 201 响应里 `createdAt`、`updatedAt` 为 0。

### 第二次 Codex 评审（针对实现）后的修正

指南里目标版本的选择依据改为 manifest 的 `clientMajor`；“只会有 1 个错误”收窄为“我们的测试里”，并补上手动恢复后的核对步骤；目的地页修正了测试对象清理失败的说法和字段规则（名称按字节计、endpoint 可为 http、prefix 的处理）；冒烟脚本加了请求超时、总超时和先 SIGTERM 后 SIGKILL 的清理。

### 没有验证的

Supabase 托管项目、真实的 R2 / B2、由套件脚本本身执行的 Supabase 恢复（本机客户端版本与镜像不一致，后三次恢复是手动执行同样的参数）。

---

## 第二阶段：后端修复与控制台界面（2026-10-08）

用户决定：给恢复套件加 Supabase 模式、修复全部后端问题、做控制台的目的地界面。

| 项 | 做法 | 验证 |
|---|---|---|
| 恢复套件 Supabase 模式 | 套件内含 `generic` / `supabase` 两条路径，默认值在生成时确定，`SUPABACKUP_PROFILE` 可覆盖。Supabase 路径：先查超级用户、再查归档里的扩展目标库能否安装（都在写入之前），然后按编辑过的目录恢复，只跳过 `graphql_public.graphql` 的授权，仍然带 `--exit-on-error` | 在 `supabase/postgres:15.8.1.085` 上由产品完成备份、上传、下载，再用套件恢复到另一台同镜像服务器：完成，数据校验和一致 |
| 平台判定 | 主机名可识别时以主机名为准，否则沿用注册时选择的平台（自托管 Supabase 也能拿到 Supabase 套件） | 单元测试 |
| 删除仍被绑定的目的地 | 拒绝，返回 `409 in_use`；绑定语句自身带“目的地仍存在”的条件，堵住并发窗口 | 单元测试 + 实测 |
| 下载地址接口 | 目的地已删除时返回 `409 destination_removed`，不再是空的 500 | 代码路径 |
| `verifyReadback` | 省略时按接口文档存为 `true` | 实测 |
| 创建响应的时间戳 | 返回真实的 `createdAt` / `updatedAt` | 实测 |
| 测试对象清理失败 | 现在会让连通性测试失败 | 单元测试 |
| 控制台“存储”页 | 目的地的添加 / 测试 / 对账 / 删除，以及按数据库选择目的地；接口新增 `Database.destinationId` | 浏览器实测（中英文、手机宽度） |

Codex 评审（针对本阶段）5 条，均已修正：目录过滤改为按记录类型精确匹配（对象名是被备份库里的数据，不能参与匹配）并补了伪造名称的回归测试；绑定与删除的并发窗口；目的地列表加载失败时下拉框误显示“无”；套件说明里对预检范围的表述。

检查结果：后端全部测试、golangci-lint、控制台类型检查 / lint / 31 个单元测试、5 个 Playwright 端到端测试、文档站构建与 52 项冒烟均通过。未运行：`oasdiff` 契约兼容检查（本机未安装；改动只有新增字段和说明文字）。

仍未验证：Supabase 托管项目；真实的 R2 / B2；busybox `ash` 下的套件脚本（本机与镜像的 `sh` 都是 dash）。
