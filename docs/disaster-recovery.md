# 灾难恢复场景（Phase 8 升级门禁 P0-08）

> 每个场景给出：会发生什么、你能恢复什么、操作步骤。SupaCove 的设计原则
> 是**任何单点故障都不应让备份文件本身不可恢复**（协议 B：应用密钥与备份
> 密钥彻底分离）。

## 场景 1：SQLite 数据库损坏或数据卷丢失（`supacove.db` / WAL 不可读；从旧版本升级后文件名已从 `supabackup.db` 迁移）

**会怎样**：任务历史、调度配置、注册的库与目的地、通知设置全部丢失。
**备份文件**：完好——密文在桶里/暂存目录里，与 SQLite 无关。

恢复步骤：

1. 重新启动 SupaCove（空数据目录），bootstrap 管理员。
2. `supabackup age show` 会提示未配置——**不要**重新 `age init`（会生成新
   密钥对）；旧密文仍然只能用原 recipient 解密。把原 recipient 重新写回：
   `INSERT INTO settings (key,value) VALUES ('age_recipient','<原recipient>'),('age_key_id','<原指纹>');`
   （recipient 是公钥，从你的 manifest 或旧记录里都能找到。）
3. 重新注册数据库与目的地；重建调度。
4. 旧密文可直接下载恢复（见场景 3）；不受本场景影响。

## 场景 2：应用主密钥不可用（`secret.key`）

分三种情况，先尝试**找回原文件**（找回 = 一切照旧，无需任何重建）：

1. **文件丢失**（存在过但没了）：启动时会生成一个**新**密钥，SQLite 里的
   旧凭据从此不可解密。
2. **文件格式损坏**（截断/非 64 位 hex/权限过宽）：启动**直接失败**
   （LoadOrCreateSecret 拒绝），实例不能起来。修复权限或换回好文件。
3. **文件完好但内容被换**：能启动，但所有旧凭据解密失败。

情况 2/3（以及无法找回原文件的情况 1；格式损坏的文件需先安全移除才能
启动生成新密钥）的重建清单（丢失的不只是连接串）：

- **每个注册的数据库**：删除后用连接串重新注册；
- **每个存储目的地**：同样以主密钥加密——访问密钥不可读，目的地会报
  "destination secret unreadable … re-create the destination"。重建目的地、
  重新指派到各库、跑一次诊断测试；
- 重新执行 `age verify --identity-file …` 确认备份密钥不受影响（协议 B：
  它从不经过主密钥）；
- 之后跑一次手动备份验证远端提交恢复。

**备份文件**：完好——主密钥从不参与备份文件加密（协议 B）。

## 场景 3：整台实例丢失，只剩桶里的密文 + 离线 age 私钥

**这是设计保证的最坏情况**——恢复完全不依赖 SupaCove：

**优先使用恢复套件**（自动完成下列所有检查）：

```bash
AGE_IDENTITY_FILE=/secure/age-identity.txt PGPASSWORD='…' \
  sh restore.sh "postgresql://user@host:5432/restored" ./backup.dump.age
```

手动等价流程（任何一步失败都必须停止；umask 077；密码不进 argv）：

```bash
set -eu
umask 077
# 1. 从桶下载 <prefix>/backups/<backup_uuid>.dump.age 与 .manifest.json
# 2. 必做：密文哈希校验（协议 C.1 的哈希是检错，不是签名；失败即停止）
echo "$(python3 -c "import json;print(json.load(open('backup.dump.age.manifest.json'))['archive']['sha256'])")  backup.dump.age" | sha256sum -c -

# 3. 解密（私钥是唯一的钥匙）到受限临时目录
AGE_IDENTITY=age-identity.txt
TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT
age --decrypt -i "$AGE_IDENTITY" -o "$TMP/restored.dump" backup.dump.age

# 4. 恢复到全新数据库（createdb 的位置参数是库名，连接走 --maintenance-db）
TARGET="postgresql://user@host:5432/restored"
createdb --maintenance-db="postgresql://user@host:5432/postgres" restored
pg_restore --exit-on-error --no-owner -d "$TARGET" "$TMP/restored.dump"

# 5. 对照 manifest 声明的表数量基线
psql "$TARGET" -c "SELECT count(*) FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema')"
```

表数核对只是初步检查，不等于完整的数据一致性验证；正式演练用
`supabackup` 内嵌验证或逐表断言。

manifest.json 内含 dump 工具版本、服务端版本、依赖扩展/角色、表数量基线
与源库物理体积，恢复前先读它。`restore.sh` 恢复套件把以上流程（含哈希
校验、空目标检查、表数核对）自动化，可在**没有任何 SupaCove 组件**的
主机上用 sh + age + pg_restore 执行。

## 场景 4：升级失败（迁移中断/新版本起不来）

- 启动前自动备份：每次迁移前对 `supacove.db` 做带版本号的
  `pre-migrate-v<源版本>-<时间戳>.db` 快照；按**升级批次**保留（每个源版本
  保留最新一份，上限 5 个批次），存放在数据目录的 `backups/` 子目录。
- 回滚：停止新版本 → 把最近的 pre-migrate 快照恢复为**目标版本认识的文件名**
  （回滚到改名 supabackup→supacove 之前的版本→恢复为 `supabackup.db`；回滚到
  改名之后的版本→恢复为 `supacove.db`。恢复错了名字，旧版本会另建空库、看起来
  像数据丢失，且下次升级会因新旧主库并存而拒绝启动，届时删除错误的空库/保留
  完整库即可）→ 同时把目录里现有的 `supacove.db{,-wal,-shm}` 与
  `supabackup.db{,-wal,-shm}` **两组三件套都移走/隔离**（只留按目标名称恢复出的
  快照，避免任何一侧残留触发下次升级的双主库拒绝）→ 启动上一个正常版本。
  goose 版本表随快照一起回退。
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
- **备份 SupaCove 自己的 SQLite 到桶**：实例元数据以场景 1 的方式重建；
  备份文件本身从不依赖它。
