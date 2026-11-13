# ARTEX 到 Ghoul 手动迁移指引

> 适用对象：已有原版 ARTEX 数据，希望迁移到当前 Ghoul 版本的用户。
>
> 核心原则：**逻辑备份 PostgreSQL，迁移 `data/` 与 `skills/`，首次启动由 Ghoul 自动补齐新 schema。**
>
> 不要直接复制 PostgreSQL 数据目录、Docker volume 或 `pgdata/`。

## 1. 迁移范围

需要迁移：

- PostgreSQL 全部业务数据
- `data/tasks/`
- `data/sessions/`
- `data/transcripts/`
- `data/drafts/`
- `data/traffic/_ca/`
- `data/traffic/_index/`
- `data/traffic/_blobs/`
- `skills/`
- 可选的 `jwt.key`

通常不需要迁移：

- 源 `pgdata/` 或 PostgreSQL Docker volume
- `.delete-staging/`
- `traffic/_delete_staging/`
- `core.*`
- 浏览器 profile、编译缓存、`__pycache__`、临时文件
- 源 `.env` 原文件

数据库中的 `settings`、`agent_prompts`、`tools`、`agents`、`llm_profiles` 等配置会随完整数据库备份迁移。

## 2. 变量约定

以下命令使用两个目录变量：

```bash
ARTEX_ROOT=/path/to/ARTEX
GHOUL_ROOT=/mnt/e/SecTools/AI-Agent/Ghoul
DUMP_FILE=/tmp/artex-migration.dump
```

如源 ARTEX 在远程虚拟机，建议先通过 SSH 登录源机执行备份，再将文件传到目标机。

## 3. 迁移前检查

检查版本、磁盘和容器：

```bash
cd "$ARTEX_ROOT"
docker compose ps
docker compose exec -T postgres postgres --version
df -h "$ARTEX_ROOT"
```

确认源数据库版本不超过目标版本。当前 Ghoul 使用 **PostgreSQL 16**。

必须使用 PostgreSQL 16 的 `pg_dump`。PostgreSQL 17/18 生成的归档可能无法被 PostgreSQL 16 的 `pg_restore` 读取。

## 4. 源端停机

先暂停所有未完成任务，再停止 ARTEX 应用。保留 PostgreSQL 运行，直至数据库备份完成。

```bash
cd "$ARTEX_ROOT"
docker compose stop artex
```

如果源端仍有可能运行 Planner/Worker 的进程，也应确保它们已停止。

## 5. 备份 PostgreSQL

源 ARTEX Compose 服务名通常为 `artex`，数据库用户和库名以源 `.env` 为准。以下示例使用 `artex/artex`：

```bash
cd "$ARTEX_ROOT"

docker compose exec -T postgres \
  pg_dump -U artex -d artex \
  -Fc -Z6 --no-owner --no-privileges \
  > "$DUMP_FILE"

sha256sum "$DUMP_FILE" > "$DUMP_FILE.sha256"
```

确认备份非空：

```bash
ls -lh "$DUMP_FILE" "$DUMP_FILE.sha256"
```

如果源端没有 PostgreSQL 16 客户端，可在任意有 Docker 的机器上临时使用 `postgres:16-alpine` 执行 `pg_dump`。

## 6. 准备目标 Ghoul

```bash
cd "$GHOUL_ROOT"
cp .env.example .env
sed -i 's/\r$//' .env
chmod 600 .env
```

至少配置：

```dotenv
POSTGRES_USER=ghoul
POSTGRES_PASSWORD=<新的数据库密码>
POSTGRES_DB=ghoul
GHOUL_TAG=migrated
```

旧变量建议改为 Ghoul 变量：

| ARTEX 旧变量 | Ghoul 变量 |
|---|---|
| `ARTEX_TAG` | `GHOUL_TAG` |
| `ARTEX_LLM_PROVIDER` | `GHOUL_LLM_PROVIDER` |
| `ARTEX_LLM_MODEL` | `GHOUL_LLM_MODEL` |
| `ARTEX_LLM_BASE_URL` | `GHOUL_LLM_BASE_URL` |
| `ARTEX_LLM_PROXY` | `GHOUL_LLM_PROXY` |
| `ARTEX_LLM_STREAM` | `GHOUL_LLM_STREAM` |
| `ARTEX_NOTIFY_ALLOW_LOCAL` | `GHOUL_NOTIFY_ALLOW_LOCAL` |
| `ARTEX_BTW_MAX_OUTPUT_TOKENS` | `GHOUL_BTW_MAX_OUTPUT_TOKENS` |

Ghoul 仍兼容多数 `ARTEX_*` 变量，但建议新部署统一使用 `GHOUL_*`。

如果使用本地 `config.json`，可通过 `GHOUL_CONFIG` 指定路径。数据库连接也可以使用 `GHOUL_PG_DSN` 覆盖。

## 7. 恢复数据库

目标数据库必须是空库。只启动 PostgreSQL：

```bash
cd "$GHOUL_ROOT"
docker compose --env-file .env up -d postgres
```

等待 PostgreSQL 健康后恢复：

```bash
docker compose --env-file .env exec -T postgres \
  pg_restore -U ghoul -d ghoul \
  --no-owner --no-privileges \
  --exit-on-error --single-transaction \
  < "$DUMP_FILE"
```

恢复后、启动 Ghoul 前，暂停所有未完成任务：

```bash
docker compose --env-file .env exec -T postgres \
  psql -U ghoul -d ghoul -c \
  "UPDATE tasks SET paused=true, queued=false
   WHERE status NOT IN ('done','failed') AND deleted_at IS NULL;"
```

不要使用 `--clean` 清理目标库。

## 8. 迁移数据目录

推荐使用 root 或具备读取权限的账号执行，并保留所有权、时间、符号链接和 ACL：

```bash
sudo rsync -aHAX --numeric-ids \
  "$ARTEX_ROOT/data/" \
  "$GHOUL_ROOT/data/"
```

远程迁移示例：

```bash
rsync -aHAX --numeric-ids \
  --rsync-path="sudo -n rsync" \
  kali@<ARTEX_HOST>:"$ARTEX_ROOT/data/" \
  "$GHOUL_ROOT/data/"
```

不要使用 `chmod -R 777`。

建议权限：

- 普通目录：`750`
- 普通文件：`640`
- 需要执行的任务文件：`750`
- `.env`、`jwt.key`、CA 私钥：`600`
- `pgdata/`：UID 70、权限 `700`

必须保留 `data/traffic/_ca/`。它是录制代理使用的 CA 证书和私钥，丢失后旧客户端证书和部分历史流量回放能力会受影响。

## 9. 合并 skills

不要使用 `--delete`，避免删除 Ghoul 新增的 C2 等内置技能：

```bash
sudo rsync -aHAX --numeric-ids \
  "$ARTEX_ROOT/skills/" \
  "$GHOUL_ROOT/skills/"
```

源 ARTEX 同名 skill 会覆盖，Ghoul 独有 skill 会保留。

## 10. 迁移 JWT key

如果源 ARTEX 使用 `data/jwt.key`，复制到 Ghoul 根目录：

```bash
cp "$ARTEX_ROOT/data/jwt.key" "$GHOUL_ROOT/jwt.key"
chmod 600 "$GHOUL_ROOT/jwt.key"
```

如果源端没有该文件，Ghoul 首次启动会自动生成。管理员密码哈希在数据库中，完整恢复数据库后原管理员密码继续有效。

## 11. 处理权限

如果使用 WSL 的 `/mnt/e` 等 DrvFs 目录，建议在 `/etc/wsl.conf` 启用 metadata：

```ini
[automount]
enabled=true
options = "metadata,umask=022,fmask=011"
```

修改后需要执行 `wsl --shutdown` 并重新进入 WSL。

不要在启用 metadata 前对目录执行批量 `chmod`，否则可能得到与预期不一致的权限。

## 12. 启动 Ghoul

```bash
cd "$GHOUL_ROOT"
docker compose --env-file .env build ghoul
docker compose --env-file .env up -d
docker compose --env-file .env logs --tail=300 ghoul
```

首次启动时 Ghoul 会执行 `schema.sql`，在源 schema 基础上补齐新表和字段。

重点检查日志中是否出现：

- schema 执行失败
- deadlock
- constraint violation
- foreign key violation
- extension missing

## 13. 数据验收

健康检查：

```bash
curl -fsS http://127.0.0.1:8787/api/health
```

核对关键表行数：

```sql
SELECT 'tasks', count(*) FROM tasks
UNION ALL SELECT 'conversations', count(*) FROM conversations
UNION ALL SELECT 'activity', count(*) FROM activity
UNION ALL SELECT 'conversation_activities', count(*) FROM conversation_activities
UNION ALL SELECT 'exploration_nodes', count(*) FROM exploration_nodes
UNION ALL SELECT 'exploration_edges', count(*) FROM exploration_edges
UNION ALL SELECT 'assets', count(*) FROM assets
UNION ALL SELECT 'findings', count(*) FROM findings
UNION ALL SELECT 'finding_retests', count(*) FROM finding_retests
UNION ALL SELECT 'llm_usage', count(*) FROM llm_usage
UNION ALL SELECT 'tool_usage', count(*) FROM tool_usage
UNION ALL SELECT 'server_logs', count(*) FROM server_logs;
```

文件数量核对：

```bash
find "$GHOUL_ROOT/data/tasks" -type f | wc -l
find "$GHOUL_ROOT/data/sessions" -type f | wc -l
find "$GHOUL_ROOT/data/transcripts" -type f | wc -l
```

人工核验：

- 管理员原密码可登录
- 历史会话可打开
- 探索图可加载
- 历史 Finding 的文本 evidence 和 report 可读取
- `conv-*` transcript 可加载
- LLM Profile 可查看，模型配置正确
- Planner/Worker 任务保持暂停
- 录制代理和 CA 相关页面正常

## 14. Windows/WSL 大小写冲突

NTFS 默认不区分大小写。如果源数据中存在仅大小写不同的文件名，例如 `a.PNG` 与 `a.png`，它们不能同时作为普通文件存在于 `/mnt/e`。

处理方式：

1. 优先将 `data/` 放在 WSL ext4 文件系统。
2. 如果必须放在 `/mnt/e`，将冲突文件保存为 tar 归档，并保留相对路径清单。

示例：

```bash
tar -C "$ARTEX_ROOT/data" -cpf \
  "$GHOUL_ROOT/migration-backups/data-case-collisions.tar" \
  -T case-collisions.manifest
```

## 15. 回滚与保留

源 ARTEX 至少保留 7 天：

- 不删除源 VM
- 不删除源 PostgreSQL volume
- 保留源库 dump
- 保留源 `data/`

建议保留：

- 一个源 ARTEX 数据库 dump
- 一个目标 Ghoul 迁移前数据库 dump
- 大小写冲突 tar 与清单

确认 Ghoul 数据、登录、历史会话、Finding 和 LLM 配置均正常后，再清理迁移备份。

## 16. 最终检查清单

- [ ] 源 ARTEX 应用已停止
- [ ] PostgreSQL 16 逻辑备份已生成并校验 SHA-256
- [ ] 目标数据库为空库
- [ ] 数据库恢复成功
- [ ] 未完成任务已暂停
- [ ] `data/` 已完整迁移
- [ ] `data/traffic/_ca/` 已保留
- [ ] `skills/` 已合并且未删除 Ghoul 新技能
- [ ] JWT key 已按需迁移
- [ ] `.env` 已使用 Ghoul 变量和新数据库密码
- [ ] Ghoul schema 自动升级无错误
- [ ] 关键表行数一致
- [ ] 任务、会话和 transcript 文件数量一致
- [ ] 管理员密码可登录
- [ ] 历史会话、探索图和 Finding 可读取
- [ ] LLM Profile 正常
- [ ] 源环境保留至少 7 天

更完整的迁移背景和数据统计方法见 [ARTEX 数据迁移方案](./docs/ARTEX数据迁移方案.md)。
