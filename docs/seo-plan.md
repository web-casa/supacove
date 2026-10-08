# SupaCove 文档站 SEO / GEO 优化方案

状态：v2.1（决策已确认；第 5 节“基础”阶段已实施，内容阶段未开始）　日期：2026-10-08
范围：`docs-site/`（https://supacove.com）

## 0. 目标与约束

- **目标**：在“Supabase 备份”与“PostgreSQL / pg_dump 备份”两组查询上获得自然流量，
  并让生成式搜索（ChatGPT、Perplexity、Google AI 功能）能正确理解和引用本站内容。
- **约束**
  - 每条产品事实都必须能在现有文档或代码里找到出处。
  - 关于第三方平台的陈述必须附官方来源链接和人工核对日期；核对日期与 git 修改时间是两回事。
  - Aiven、Prisma Postgres、TigerData、Miget、Render **尚未实测**。这条限定同样适用于
    **已经上线在首页**的文案（标题轮播、流向图下的说明、特性卡），不只适用于未来的指南页。
  - 不为“看起来可被引用”而编造数字、评价或评分。
  - 不引入新依赖，除非 Next.js / Fumadocs 自带能力做不到。

## 1. 依据

### 1.1 关键词数据

来源：DataForSEO，采集于 2026-10-08。

| 项 | 取值 |
|---|---|
| 搜索量 | `keywords_data/google_ads/search_volume/live`；“美国”= location 2840、language en；“全球”= 不指定地区和语言 |
| 关键词建议与难度 | `dataforseo_labs/google/keyword_suggestions/live`，美国 / en；难度取 `keyword_properties.keyword_difficulty`（自然搜索难度估计） |
| 竞争度 | Google Ads 的 `competition` 字段，是**广告**竞争，不代表自然搜索难度 |

| 关键词 | 美国 | 全球 | 难度 | 用途 |
|---|---|---|---|---|
| pg_dump / pg dump | 1,600 | 12,100 | — | 教程意图，范围宽 |
| postgres backup 及变体 | 210–260 | 1,900 | 5–33 | 品类词 |
| supabase backup(s) | 170 | 1,000 | 2 | **首选**：与产品最对口 |
| postgres backup and restore | 90 | — | 5–17 | 教程意图 |
| supabase db dump | 70 | 320 | — | 官方 CLI 命令名 |
| how to backup supabase database | 20 | 110 | — | 直接对口 |
| supabase project paused | 20 | 110 | — | 免费版相关 |
| postgres backup to s3 | 20 | 70 | 0 | 对口、范围窄 |
| pgbackrest / databasus / simplebackups | 590 / 110 / 110 | 5,400 / 1,600 / — | — | 竞品品牌词，多为导航意图 |
| neon / railway / render + backup | 各约 10 | 40–50 | — | 长尾 |

读数时的限定：

- 变体之间高度重叠，**不能相加**；美国与全球两列不可混成一个优先级分数。
- 难度 2 只是工具估计，**不等于“几乎没有竞争”**；CPC 高也不代表自然流量机会。
  这些都是待 SERP 人工核对的假设。
- 搜索量为 0 或 10 的是分档值，不能证明没有需求。
- “supabase db dump”有量不能证明用户在找替代方案。
- 样本里中文词量很小，但这只是一个小样本，且不覆盖百度。
- 站点未上线，没有任何真实排名数据。

“Supabase 备份”作为第一个内容方向保留，理由是**产品契合度**，不是数据本身。

### 1.2 SEO / GEO 实践

| 结论 | 来源 |
|---|---|
| 在 Google 看来 AEO / GEO 仍是 SEO；生成式搜索不需要专门的结构化数据；Google 不使用 llms.txt | [Google：AI 功能优化指南](https://developers.google.com/search/docs/fundamentals/ai-optimization-guide) |
| FAQ、HowTo 不在 Google 当前支持的富结果类型里（Schema.org 词汇本身仍存在） | [Google 搜索更新日志](https://developers.google.com/search/updates) |
| hreflang 需要成对互指并包含自身；`x-default` 可独立指定 | [Google：本地化版本](https://developers.google.com/search/docs/specialty/international/localized-versions) |
| Googlebot 通常不发送 `Accept-Language`；按语言自动跳转不是排名前提 | [Google：按语言区域自适应的页面](https://developers.google.com/search/docs/specialty/international/locale-adaptive-pages) |
| robots 屏蔽不等于禁止索引；`noindex` 必须能被抓取到才生效 | [Google：阻止索引](https://developers.google.com/search/docs/crawling-indexing/block-indexing) |
| AI 生成的内容（含标题、描述、结构化数据、alt）发布前需人工核对 | 同第一行 |

以下只作为**编辑习惯**采用，不当作因果规律：答案先行、实体名称首次出现用全称并保持一致、
涉及事实处给出可核对的来源。初稿里“AI Overviews 偏向前 10”“社区平台占 Perplexity 引用近半”
“llms.txt 几乎无人请求”等说法来自第三方博客，方法不明，已从依据中移除。

**爬虫标识按用途区分**（初稿把“被引用”和“允许训练”混为一谈，已更正）：

| 用途 | 标识 |
|---|---|
| 搜索发现 | Googlebot、Bingbot、OAI-SearchBot、Claude-SearchBot、PerplexityBot |
| 模型训练 | GPTBot、ClaudeBot |
| 用户触发的抓取 | ChatGPT-User、Claude-User、Perplexity-User |
| Google 其他 AI 用途 | Google-Extended（只是 robots 里的控制标记，**没有独立的爬虫 UA**，不影响 Google 搜索收录与排名） |

是否允许训练类爬虫是独立的政策决定，不是被引用的前提。CDN / WAF 的拦截同样会影响这些抓取。

## 2. 现状问题

| # | 问题 | 位置 |
|---|---|---|
| P1 | 首页 `<title>` 为“supabackup docs / supabackup 文档”，无关键词 | `app/[lang]/layout.tsx` |
| P2 | 无 `metadataBase`、canonical、hreflang、sitemap、robots | 同上 |
| P3 | 文档页的 `generateMetadata` 只返回标题和描述，OG 标题继承自布局的通用值 | `app/[lang]/docs/[[...slug]]/page.tsx` |
| P4 | 品牌是 SupaCove，站内全部写作 supabackup | 见 4.1 的清单 |
| P5 | 首页 H1 的可见默认信息只有一个平台名；其余平台名在隐藏的测量节点和 `aria-label` 里（HTML 里并非没有，初稿此处写错） | `components/rotator.tsx` |
| P6 | 没有页面正面回答“怎么备份 Supabase 数据库” | `content/docs` |
| P7 | 无社交分享图、无结构化数据 | — |
| P8 | 未启用 git 最后修改时间；没有机器可读的纯文本版本 | `source.config.ts` |
| P9 | 首页目录和文档页的“03 / 12”编号只识别顶层页面，新增子目录不会出现 | `components/home.tsx`、文档页组件 |

初稿把“文档页标题是功能名”列为问题，已撤回：frontmatter 标题同时决定 H1、侧栏、首页目录和站内搜索，
功能名本身不是缺陷（见 4.5）。

## 3. 需要先确认的决策

| # | 决策 | 选项 | 建议 |
|---|---|---|---|
| D1 | **SupaCove 与 supabackup 的关系** | A. SupaCove 只是网站品牌：写作“SupaCove — supabackup 的文档”，产品描述和 `SoftwareApplication.name` 仍是 supabackup。B. SupaCove 是产品品牌：正文称 SupaCove，并说明可执行文件、镜像、环境变量、仓库标识仍为 `supabackup` / `SB_*` | 需负责人定。未确认前不写“原名 supabackup”，也不用 `alternateName` 暗示改名 |
| D2 | **部署形态** | A. Next 服务端 + 预渲染页面（现状：`next.config.mjs` 未设 `output: "export"`，且有依赖请求的 `/api/search`）。B. 纯静态导出 + 托管层重定向 + 静态或客户端搜索 | 两者不可互换，影响 D3、robots、llms 路由、OG 图的实现方式 |
| D3 | **根路径 `/`** | A. 固定重定向到确认的默认语言（HTTP 重定向）。B. 仅在 `/` 做语言协商：服务端或边缘运行时、302/307、英文兜底、缓存按 `Accept-Language` 区分 | 建议 A 且默认英文。`/en`、`/zh` 始终各自预渲染，显式语言路径永不协商或跳转 |
| D4 | **首选域名** | `supacove.com` 或 `www.supacove.com` | `supacove.com`，另一个 301 过来 |
| D5 | **训练类爬虫** | 允许或禁止 GPTBot、ClaudeBot、Google-Extended | 文档站建议允许；与搜索类爬虫分开决定 |

### 3.1 已确认的决策（2026-10-08）

| # | 结论 |
|---|---|
| D1 | 产品名与品牌名都改为 **SupaCove**（supabackup.com 已被注册）。本轮只改站点和文档里的名称；二进制、镜像、指标名、请求头、`SB_*` 仍是 `supabackup`，后端改名另行安排 |
| D2 | Next 服务端 + 预渲染页面 |
| D3 | 英文不带前缀（`/`、`/docs/…`），中文在 `/zh`；访问 `/` 时中文浏览器跳到 `/zh`，显式选过语言后不再跳；`/en/…` 308 到无前缀地址 |
| D4 | `https://supacove.com`（不带 www） |
| D5 | 允许训练类爬虫：robots 只有一个放行全部的通配组 |

因 D3，下文示例里的 `/en/…` 地址实际均为无前缀形式，`x-default` 指向无前缀的英文页。

**“基础”阶段的实施结果**：`proxy.ts`（语言路由）、`lib/site.ts`（统一 URL 构造）、逐页 canonical / hreflang /
Open Graph / Twitter 卡片、`app/sitemap.ts`、`app/robots.ts`、搜索接口 `X-Robots-Tag: noindex`、
git 最后更新日期、首页与文档页 JSON-LD、单张品牌分享图、站点与文档正文改名。

**内容阶段第一步（同日）**：`guides/supabase-backup` 中英文已发布并接入侧栏、首页目录和 sitemap；
首页新增“常见问题”区块、套餐卡片到指南的链接，副文案加入 `pg_dump` 与 S3 兼容；注册数据库、快速开始两页反向链接到指南。
指南依据现有文档、`backend/internal/platform/platform.go` 的恢复说明和 Supabase 官方备份文档写成，
**尚未在真实 Supabase 项目上实测**，这与 4.4 的要求不符，实测后需复核。

路由实现改为每种语言一棵独立的路由树（`app/(en)`、`app/zh`，共享实现在 `routes/`）：
最初用地址重写把无前缀路径映射到 `/en/…`，会让预渲染页面在浏览器端出现渲染不一致，已弃用。

**第二轮 Codex 评审后的修正（同日）**：404 改为全局静态文档（`app/global-not-found.tsx`，服务端渲染）；
`/en` 入口记录显式的英文选择；语言跳转保留查询串、忽略 `q=0`；标题轮播遵循“减少动态效果”；
指南补上恢复前的角色 / 扩展 / 版本准备并标注“尚未完整实测”；五个未实测平台在首页和文档里加了限定语。
仓库根 README、`docs/` 下的部署 / 灾备 / 容量文档和控制台界面已改名为 SupaCove。

S3 指南暂缓：存储目的地目前只有 API（`/destinations`），控制台没有配置界面，用户文档里也没有对应页面，
缺少可引用的事实来源。

尚未做：4.2 第 8 项（首页 H1 仍是轮播标题）、第 11 项（托管加固，属部署层）、4.3 的机器可读导出、
4.4–4.6 的全部内容工作、4.8 的度量接入、仓库根 README 与 `docs/` 下其他文档的改名。

## 4. 方案

### 4.1 品牌迁移清单

改之前先把每处出现归为三类之一：**展示品牌**、**产品名称**、**技术标识**。
命令、镜像引用、卷名、配置示例、`SB_*` 属于技术标识，一律保留。

需要逐一处理的位置：`components/logo.tsx`、`components/hero.tsx`（流向图中心节点）、
`components/home.tsx`（页脚品牌和大字水印）、`lib/home-copy.ts`、`app/[lang]/layout.tsx`
（标题、描述、OG `siteName`）、两种语言的全部文档、仓库根 `README.md`、`docs-site/README.md`、
`app/icon.svg`、后续的 OG 图、公开仓库描述。

- 现有的圆柱图标是否保留，单独决定；换品牌不等于必须换图标。
- 站点与仓库互相链接，并附一句身份说明；**不得暗示与 Supabase 有官方关系**。

### 4.2 技术 SEO

1. **统一的 URL 构造函数**，供页面元数据、sitemap、JSON-LD、OG 共用。以
   `/zh/docs/guides/supabase-backup` 为例：
   - canonical：`https://supacove.com/zh/docs/guides/supabase-backup`（译文页自指，**不**指向英文）
   - `zh`：该中文地址
   - `en` 与 `x-default`：`https://supacove.com/en/docs/guides/supabase-backup`

   两种语言的页面输出同一组互指链接（含自身）。`x-default` 指向**对应的英文页**，不是 `/en` 首页，
   且与 Fumadocs 的 `defaultLanguage` 无关。canonical 不能只写在 `[lang]/layout.tsx` 里，否则会把
   语言首页的地址传给所有子页。
2. **URL 规范化**：沿用现有的无尾斜杠约定；强制 HTTPS 和单一主机名；永久性的主机 / 路径规范化用
   单跳 301/308。sitemap、canonical、alternate、站内链接一律使用最终返回 200 的地址。
   canonical 去掉跟踪参数。改标题或换品牌本身不需要重定向。
3. **元数据**：首页元数据放在 `[lang]/page.tsx`，文档元数据放在文档页；`%s · <品牌>` 只作为子页标题模板。
   每页返回完整的 `openGraph`（标题、描述、URL、站名、语言）和 Twitter 卡片字段
   （Next 的元数据是浅合并，布局里的通用 OG 标题不会被页面标题自动替换）。
   描述要求：每页唯一、准确、已本地化；长度是编辑目标，不是 Google 的硬限制。
4. **标题**（英文首页候选，按 D1 定稿）
   - D1-A：`SupaCove — docs for supabackup, self-hosted Supabase & Postgres backups`
   - D1-B：`SupaCove — Self-hosted Supabase & Postgres backup to your own S3`
5. **sitemap**（`app/sitemap.ts`）：两种语言的首页与全部文档页，带 `alternates.languages`。
   若 `/` 是重定向，则不列入。`/en` 与 `/en/docs` 是用途不同的两个页面，各自自指。
   `lastModified` 取自 `page.data.lastModified`；**日期未知时省略，绝不用构建时间代替**。
   首页不是 MDX 文档，日期来源单独定义。
6. **git 日期**：`defineDocs({ docs: { lastModified: true } })`；构建环境需要完整 git 历史
   （当前 CI 的 docs 任务未设 `fetch-depth: 0`，需补）。文档页用语义化的 `<time>` 显示“最后更新”。
7. **robots 与 noindex**
   - 优先只用一个通配组；只有在 D5 决定区别对待时才写针对特定爬虫的组，
     并在这些组里重复适用的限制（特定组不会继承 `*` 组的规则）。
   - `/api/search`：返回 `X-Robots-Tag: noindex`，并**允许抓取**该地址，否则爬虫读不到这个头。
     不把搜索地址放进 sitemap，也不生成可抓取的带查询参数的链接。
   - 不存在的页面必须返回真正的 404 状态并带 `noindex`，不能只检查自定义 404 组件能否显示。
8. **首页 H1**：使用简洁、可见、服务端渲染的 H1 表达核心定位（Supabase / PostgreSQL 备份）。
   轮播只作装饰；带限定条件的平台列表放在可见的辅助内容里。
   不添加隐藏的关键词文本，也不把八个平台名塞进 H1。
9. **结构化数据**（用于如实描述实体；富结果资格取决于各功能自身的要求）
   - 首页：`WebSite`、发布者实体（仅当确有其组织时才用 `Organization`）、`SoftwareApplication`
     （名称按 D1；真实的许可证 URL；如实的运行环境；`offers.price: 0` 只针对软件本身，不暗示托管免费）。
     Google 的软件富结果还要求评分或评价，本站**没有**，不为此编造。
   - 文档页：`TechArticle`（headline、description、inLanguage、dateModified）与 `BreadcrumbList`。
     面包屑里的每个 URL 都必须可访问；不为不存在的 `/guides` 落地页生成面包屑。
   - 各实体用稳定的 `@id` 互相关联。不加 FAQPage / HowTo。
10. **OG 图**：首发每种语言一张品牌图即可，逐页图片延后。用 `next/og` 的 `ImageResponse`，1200×630，
    带 alt。中文图需要支持中文的字体（现有 Geist 只含拉丁字符）。在 D2 确定的部署形态下逐一验证图片地址。
11. **托管加固**（运维卫生，不当作排名手段）：生产 HTTPS、合适的 HSTS、`X-Content-Type-Options`、
    referrer 策略、与 Next 和 JSON-LD 兼容的 CSP；预览部署需鉴权或明确 `noindex`，
    并确认生产环境没有继承预览的 `noindex`。

### 4.3 GEO / AEO

1. **编辑习惯**：每页开头 2–3 句直接回答标题问题；实体首次出现用全称；术语全站一致；
   已有的具体数字保留，但不为“显得可引用”而添加数字。
2. **第三方平台政策**：附官方链接和“人工核对于 YYYY-MM-DD”。
3. **机器可读版本**（可选、限时投入；Google 明确表示不影响其收录和排名）。
   Fumadocs 提供的是**辅助函数**，不是现成的端点，实际工作量包括：
   - `docs.postprocess.includeProcessedMarkdown: true`，用 `page.data.getText('processed')` 取文本；
   - `fumadocs-core/source/llms` 的 `llms`，为 `.full()` / `.page()` 提供 `renderPage`；
   - 为 `/llms.txt`、`/llms-full.txt` 和按语言的单页 Markdown 写路由，放在 `/api/` 之外；
   - 按 D2 决定是否在构建期生成；不能假设生产运行时还有源文件；
   - 标题本地化、来源 URL 用绝对地址、UTF-8，输出里不得残留 MDX 的 import 或 JSX。
4. **站外**：仓库 README 首段与站点使用同一套实体表述并链接到 supacove.com。社区问答由人工进行。

### 4.4 内容

**页面分工**（先定边界，避免新指南与现有文档互相抢词）：

| 页面 | 主要职责 |
|---|---|
| 首页 | 产品与品类概览 |
| Supabase 指南 | Supabase 备份与恢复的完整流程 |
| 快速开始 | 第一次成功安装并完成备份 |
| 注册数据库 | 连接、权限、池化与 TLS |
| S3 指南 | S3 专属的配置与已验证流程 |
| 配置参考 | 精确的配置项参考 |
| 恢复与灾备 | 加密恢复套件与灾备流程 |
| pg_dump 指南 | 通用的导出格式、示例与恢复区别 |
| 故障排查 | 产品相关的诊断 |

指南页链接到权威的参考章节，不复述它们；不用 canonical 来弥补内容重叠。

**发布顺序**

| 顺序 | 页面 | 说明 |
|---|---|---|
| 1 | `guides/supabase-backup` | 首篇，见下方必备内容 |
| 2 | `guides/postgres-backup-s3` | 范围窄、对口，先于 pg_dump 指南 |
| 3 | `guides/pg-dump` | 范围宽，待 SERP 核对后再决定是否值得做 |
| 4 | Neon / Railway 指南 | 视前几篇的数据决定 |

初稿里的三项已调整：

- **“Supabase 免费套餐”单独成页** → 先作为 Supabase 指南里的一节，能独立回答一个明确问题时再拆。
- **一个通用“对比”页同时针对三个竞品品牌** → 取消。竞品品牌词多为导航意图，
  一页泛泛的对比不是可信的目标；确有需要时按单个竞品、基于可核对事实另议。
- **Render 及其余未实测平台的指南** → 实测前不发布。

**Supabase 指南的必备内容**（防止把“数据库逻辑导出”写成“整个项目的保护”）：

- 备份的确切范围：一次 `pg_dump` 的完整数据库导出；按 `backend/internal/platform/platform.go`
  中恢复说明的口径写清包含的 schema。
- 明确**不包含**：Storage 的文件对象、Edge Functions 代码、Auth / Storage 的服务配置、项目级设置。
- 前置条件与所需权限；连接方式（session pooler 或直连，不能用 transaction pooler）。
- 一次**实际跑通**的恢复，并说明恢复目标是普通 PostgreSQL，而不是新的 Supabase 项目。
- 软件许可免费与计算、存储、流量成本分开说明。
- 不声称定时导出能阻止项目被暂停，也不声称能恢复一个已无法访问的数据库。

**导航**：指南的实际地址是 `/{lang}/docs/guides/<slug>`。两种语言的根 `meta.json` 都要加入 `guides`，
并各自新增 `guides/meta.json` 明确排序；首页目录和页码逻辑改为能处理子目录，或者把“参考文档”与“指南”
明确分成两组。`scripts/check-parity.mjs` 已能递归子目录，无需改写；但它只比对文件名，
不检查导航可见性、译文内容、元数据和链接有效性。

### 4.5 现有页面标题

保持简洁、面向读者的标题，例如“连接数据库”“备份任务与下载”“恢复加密备份与灾备”。
需要时在 frontmatter 增加可选的 `seoTitle`，只用于 `<title>`。
slug 和现有的标题锚点保持不变。改标题不会断链，但会影响相关性、点击率和导航可用性，需记录变更日期。

### 4.6 首页

- 首屏副文案加入 `pg_dump` 与“S3-compatible”。
- 关于 3-2-1：只有在能准确说明“这多出来的一份副本在其中的角色”时才简要提及，不暗示已满足该原则。
- 新增“常见问题”区块（纯 HTML，不加 FAQPage 标记）。
- **内链**：首页链接到 Supabase 指南；每篇指南链接到相关的注册、计划、备份、恢复参考页，
  参考页反向链接回指南；语言切换保持在当前文档。每篇已发布的指南都必须能通过普通的服务端渲染
  `<a href>` 到达，进 sitemap 不能代替这一点。只在页脚放链接是不够的。

### 4.7 中文

英文获客优先，依据是现有数据集；中文文档保留，是产品支持的需要。中文 SEO（百度）不在本方案范围内。

### 4.8 度量

- 上线前尽可能先完成 Google Search Console 与 Bing Webmaster Tools 的站点验证，生产可访问后提交 sitemap。
- 接入符合隐私要求的分析或服务器日志，按语言和落地页分段，记录自然 / 引荐流量，
  以及“指南 → 快速开始”“点击仓库”等有意义的事件。
- 记录点击、展示、点击率、未被索引的原因、按页面和查询的表现。
- AI 引荐数据不完整；引用抽查只是定性样本，每次记录引擎、日期、语言、提问和被引用的 URL。

## 5. 实施阶段

1. **确认**：D1–D5，以及 4.4 的页面分工。
2. **基础**：URL 构造与元数据（4.2 的 1–4）、规范化、robots / noindex、sitemap、git 日期、
   指南导航支持、度量接入、品牌迁移（按 D1）。
3. **首批内容**：实测通过的 Supabase 指南与首页调整；随后是 S3 指南。
4. **后续**：依据数据再加指南；然后是可选项——逐页 OG 图、更完整的结构化数据、机器可读导出。

## 6. 验收

`npm run build` 通过和人工核对事实只是起点。每个阶段还需检查生成的 HTML 与生产环境的 HTTP 响应：

- canonical 与 alternate 成对互指；sitemap 中每个地址返回 200；
- 重定向为单跳；robots 生效；搜索接口带 `noindex` 头；
- `/fr`、不存在的子目录指南返回 404；`/en/docs` 不带 slug 时正常；两种语言的对应页都存在；
- OG 图地址可访问；站内链接无死链；页面为静态渲染。

渲染、性能与可访问性（两种语言的首页及有代表性的文档页、指南页）：

- 不执行脚本时正文可读；`<html lang>` 正确；只有一个可见的 H1；
- 移动端布局与导航可用；图片有描述性 alt，装饰图被排除，并预留尺寸；
- 评估轮播、动效组件、字体与水合的开销；
- 目标：LCP ≤ 2.5 秒、INP ≤ 200 毫秒、CLS ≤ 0.1。上线前看实验室数据，有真实用户数据后以其为准；
  不把 Lighthouse 分数当作真实用户的核心指标。

译文的事实核对与文件名对齐检查分开进行。

## 7. 风险

1. D1 未定时动品牌，容易把“网站改名”做成“产品改名”。
2. 第三方平台的政策会变，指南页需要定期人工复核。
3. 首页已对五个未实测平台做了宣传，实测结果可能要求回撤文案。
4. 关键词数据量级很小，前几篇内容的效果不确定，后续投入应看实际数据再定。

## 8. 更正记录

**2026-10-08：站点改为纯静态导出，部署到 Cloudflare Pages。** 本节修正上文
与部署形态相关的两处记录，其余决策（品牌、URL 结构、canonical/hreflang、
robots 策略）不变：

- **D3 部分撤销**：静态导出没有服务端运行时，`proxy.ts`（middleware）随之
  删除。`/en/…` 308 到无前缀地址由 `docs-site/public/_redirects` 承接，
  语义不变；**“中文浏览器访问 `/` 自动跳 `/zh`”的协商跳转取消**——`/`
  对所有浏览器都是英文，中文用户经头部切换器进入 `/zh`（`sc_lang` cookie
  记忆选择仍然保留）。hreflang/canonical 结构不受影响，对搜索引擎的每语
  言唯一地址承诺反而更严格（无 UA 分歧响应）。
- **“基础阶段实施结果”更新**：搜索接口从动态 `?query=` 服务端应答改为构
  建期烘焙完整 Orama 索引（`staticGET`，客户端 `type: "static"` 查询）；
  其 `X-Robots-Tag: noindex` 头改由 `docs-site/public/_headers` 设置，
  robots 放行 `/api/search` 的理由（头必须可被抓取者看到）不变。
