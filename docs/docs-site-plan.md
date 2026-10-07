# 使用文档站开发方案（Fumadocs）

日期：2026-10-07。状态：待实施 → 实施后交 Codex 评审修正。

## 目标与边界

- 在 `docs-site/` 建立独立的 Fumadocs（Next.js）文档站，承载**面向使用者**的文档：
  安装、配置、日常操作（注册/计划/通知/下载）、恢复与灾备、监控、排障。
- 仓库现有 `docs/*.md`（deployment、disaster-recovery、capacity、dev-plan、reviews）
  保持为开发/评审材料，**不搬迁、不改写**；文档站内容从其与代码实现中提炼。
- 不部署（仓库尚无 remote）；交付物为可本地构建/运行的站点 + CI 构建门禁。
  部署形态（自托管或托管平台）列为后续任务。

## 语言策略

- 双语：`zh-CN`（默认）与 `en`，页面集合完全对齐。
- 与产品 UI 的双语标准一致：新增 `docs-site/scripts/check-parity.mjs`
  校验两语 slug 集合相同，进 CI；缺失即失败。
- 默认语言在 `lib/i18n.ts` 一行可切换。

## 页面集（slug，两语同构）

| slug | 内容要点（以代码实现为准） |
|---|---|
| index | 产品定位、四条保证（失败不伪装成功/重启收敛/独立可恢复/秘密不出错误面）、管线六阶段图 |
| quickstart | Docker 一条龙：build --target runtime、bootstrap 令牌、登录、注册库、首次备份、下载 kit |
| installation | 镜像构建、源码构建（Go 1.26.6+）、compose 仅开发警示、生产需 TLS 反代（Secure cookie） |
| configuration | 全部 SB_* 环境变量表（默认值/语义/风险），含 SB_JOB_TIMEOUT、SB_FAILED_ARTIFACT_TTL_HOURS、SB_INSECURE_COOKIE 警示 |
| databases | 注册流程、四平台注意事项（Supabase 5432 非 6543、Neon 去 -pooler、Railway PUBLIC_URL、自托管 host.docker.internal）、角色权限、sslmode 指引、预检行为 |
| scheduling | cron 五段+时区、新鲜度阈值与 EXPIRED、暂停语义、重试与去重 |
| heartbeat | 死人开关：period/grace、年龄门控、`-` 禁用、空值继承服务器默认、/fail 语义 |
| notifications | webhook 三事件、outbox 重试与 dead 语义、测试投递、投递日志读法 |
| backups | 任务状态机、下载三形态（kit/本地工件/桶 presigned）、保留策略与锚点保护、暂存配额与回收 TTL |
| restore | recovery kit 用法（hash 门禁、AGE_IDENTITY_FILE）、独立恢复（无元数据库）、主密钥 vs age identity、三场景灾备入口 |
| monitoring | /metrics 需会话、指标家族语义（含改名说明）、stats 面板口径（export 分母、dump 体积为已记录样本小计）、scrape_errors 读法 |
| troubleshooting | 七错误类与排查第一步、常见 FAQ（cookie/IPv6/池化/时区）、日志与脱敏边界 |

（index 与上表共 12 项；heartbeat 独立成页以保持可链接性。）

## 技术结构

```
docs-site/
  package.json / tsconfig.json / next.config.mjs / postcss.config.mjs
  source.config.ts            # fumadocs-mdx defineDocs（双语目录）
  mdx-components.tsx          # fumadocs-ui/mdx
  lib/i18n.ts                 # I18nProvider：zh 默认 + en
  app/layout.tsx app/page.tsx app/docs/layout.tsx app/docs/[[...slug]]/page.tsx
  app/api/search/route.ts     # 全文搜索 route handler
  content/docs/{zh,en}/*.mdx + meta.json
  scripts/check-parity.mjs
  README.md                   # 本地运行说明
```

- 版本以 `npm view` 实测的兼容组合钉住（next / react / fumadocs-core / fumadocs-ui /
  fumadocs-mdx），不凭记忆猜 major。
- 根仓库接线：`.gitignore` 加 `docs-site/.next/`；Makefile 加 `docs`（dev）与
  `docs-build`；CI 加 `docs` job（npm ci → parity → next build）。

## 验收与评审

1. `next build` 绿；`next start` 冒烟：/ 、/zh/quickstart、/en/quickstart、搜索路由 200。
2. parity 脚本对故意缺页变红。
3. Codex 评审：文档事实对照代码（环境变量默认值、CLI 子命令、端口、状态语义、
   平台注意事项）、Fumadocs 用法、计划一致性、两语内容对齐度；评审后修正收敛。
4. 内容红线：不写未实现的功能（如 start 心跳、redrive API）；不承诺未验证的平台行为；
   危险操作（删库、清暂存）给出后果说明。
