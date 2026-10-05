# 灾难恢复场景（Phase 8 升级门禁 P0-08）

> 每个场景给出：会发生什么、你能恢复什么、操作步骤。supabackup 的设计原则
> 是**任何单点故障都不应让备份文件本身不可恢复**（协议 B：应用密钥与备份
> 密钥彻底分离）。

## 场景 1：SQLite 数据库损坏或数据卷丢失（`supabackup.db` / WAL 不可读）

**会怎样**：任务历史、调度配置、注册的库与目的地、通知设置全部丢失。
**备份文件**：完好——密文在桶里/暂存目录里，与 SQLite 无关。

恢复步骤：

1. 重新启动 supabackup（空数据目录），bootstrap 管理员。
2. `supabackup age show` 会提示未配置——**不要**重新 `age init`（会生成新
   密钥对）；旧密文仍然只能用原 recipient 解密。把原 recipient 重新写回：
   `INSERT INTO settings (key,value) VALUES ('age_recipient','<原recipient>'),('age_key_id','<原指纹>');`
   （recipient 是公钥，从你的 manifest 或旧记录里都能找到。）
3. 重新注册数据库与目的地；重建调度。
4. 旧密文可直接下载恢复（见场景 4）；不受本场景影响。

## 场景 2：应用主密钥丢失（`secret.key` 丢失/损坏）

**会怎样**：SQLite 里 AES-GCM 加密的**连接串凭据不可读**（登录后报错
"stored credentials are unreadable"）。调度照跑但每次备份都会失败。
**备份文件**：完好——主密钥从不参与备份文件加密（协议 B）。

恢复步骤：重新录入各数据库的连接串（删除库后重新注册）；备份链立即恢复。
历史备份照常可恢复。

## 场景 3：整台实例丢失，只剩桶里的密文 + 离线 age 私钥

**这是设计保证的最坏情况**——恢复完全不依赖 supabackup：

```bash
# 1. 从桶下载 <prefix>/backups/<backup_uuid>.dump.age 与 .manifest.json
# 2. 校验（可选但推荐）：manifest.archive.sha256 与文件一致
sha256sum backup.dump.age   # 对照 manifest

# 3. 解密（私钥是唯一的钥匙）
age --decrypt -i age-identity.txt -o restored.dump backup.dump.age

# 4. 恢复到全新数据库
createdb -h <target> restored
pg_restore --exit-on-error --no-owner -d "postgresql://user@host/restored" restored.dump

# 5. 检查
psql -d restored -c "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')"
```

manifest.json 内含 dump 工具版本、服务端版本、依赖扩展/角色、表数量基线
与源库物理体积，恢复前先读它。`restore.sh` 恢复套件把以上流程（含哈希
校验、空目标检查、表数核对）自动化，可在**没有任何 supabackup 组件**的
主机上用 sh + age + pg_restore 执行。

## 场景 4：升级失败（迁移中断/新版本起不来）

- 启动前自动备份：每次迁移前对 `supabackup.db` 做带版本号的
  `pre-migrate-v<源版本>-<时间戳>.db` 快照，保留最近数份。
- 回滚：停止新版本 → 恢复最近的 pre-migrate 快照为 `supabackup.db` →
  启动上一个正常版本。goose 版本表会随快照一起回退。
- 新版本拒绝启动时不会写业务数据（schema 兼容性门禁在监听前生效；
  旧版本二进制会被拒于新 schema，防止双重写入）。

## 演练建议（季度）

1. 每季度挑一个生产库：备份 → 从桶下载 → 按恢复套件在**新建目标**恢复 →
   数据断言 → 销毁。把 artifact ID、工具版本、结果记录下来（Phase 5/8
   发布门禁要求）。
2. 每半年做一次场景 3 的全冷恢复（换一台机器）。
3. age 私钥的离线副本至少两处异地保存；丢失 = 所有历史密文永久不可读。

## 不做的（明确边界）

- **PITR / WAL 归档**：不在产品范围。
- **备份 supabackup 自己的 SQLite 到桶**：实例元数据以场景 1 的方式重建；
  备份文件本身从不依赖它。
