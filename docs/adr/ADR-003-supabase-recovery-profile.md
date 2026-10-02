# ADR-003：Supabase 专用导出/恢复 profile（Spike 1）

日期：2026-10-03 · 状态：**待执行**（脚本已就绪，依赖真实 Supabase 测试项目）

## 背景

评审 P0-07：Supabase 新项目已含平台 schema 与托管角色，普通全量 `pg_dump -Fc` 直接回灌会遇到对象冲突与权限错误；官方流程分开处理角色、结构、数据。本 spike 用真实 Supabase 项目验证可行的恢复 profile，**必须在 P5 实现前完成**（dev-plan v2 已把它前移到 P1 执行）。

## 执行前提（需要用户提供）

1. 源 Supabase 项目（含业务表、若干 auth 用户、RLS 策略、至少一个自定义角色、pgvector 或声明支持的扩展）。
2. 目标 Supabase 新项目（同区域，空项目）。
3. 两个项目的数据库连接串（Session pooler 或直连，非事务池化）。

## 执行

```bash
export SOURCE_DB_URL='postgresql://...'   # 源项目
export TARGET_DB_URL='postgresql://...'   # 目标新项目
bash scripts/spike1-supabase-restore.sh | tee docs/adr/spike1-results.txt
```

脚本依次尝试以下 profile 并记录每个的 `pg_restore --exit-on-error` 结果：

- **P-A 官方三分**：`roles`（pg_dumpall --roles-only 变体）+ `schema`（--schema-only, 排除平台 schema）+ `data`（--data-only）分别导出、按序恢复。
- **P-B 单档 -Fc**：直接 `pg_dump -Fc`（预期失败，作为对照基线）。
- **P-C 应用 schema only**：`--schema=<user schemas>` 结构+数据（beta 声明的最小范围）。

## 待回答的问题（结论写入本 ADR）

1. 哪个 profile 能在**新建空 Supabase 项目**上以 `--exit-on-error` 零错误恢复？
2. `storage.buckets` / `auth.users` 等托管对象的冲突如何处理（排除？官方脚本？）
3. 自定义角色恢复需要什么前置（目标项目存在的角色集合）？
4. RLS 策略、跨 schema FK（public → auth.users）是否可恢复？
5. Vault 密文恢复的前置条件？

## 决策规则

- 若 P-A/P-C 可行：P5 按其实现 Supabase profile；产物仍为 pg_dump 系列格式。
- 若均不可行且必须改变产物格式：**先修订本 ADR 并由负责人决策，不得悄悄改格式**（dev-plan P0-07）。
- 若 beta 只能支持应用 schema：对外文案明确为"应用 schema 逻辑备份"，不暗示完整平台备份。

## 参考

- [官方 backup-restore 文档](https://supabase.com/docs/guides/platform/migrating-within-supabase/backup-restore)、[self-hosting 恢复说明](https://supabase.com/docs/guides/self-hosting/restore-from-platform)
