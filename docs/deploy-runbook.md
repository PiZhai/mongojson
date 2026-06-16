# 日常运维 Runbook

这份文档只负责**已部署完成之后**的日常操作。  
首次部署请先看 [docs/deploy-centos-stream-10.md](/Users/administrator/Documents/mongojson/docs/deploy-centos-stream-10.md)。

## 1. 脚本总览

默认脚本目录：

```bash
/opt/personal-tooling/app/deploy
```

可用脚本：

- `deploy-release.sh`：常规全量发版，更新代码并重建前后端与 nginx
- `deploy-frontend.sh`：仅前端发版，更新代码并重建 `frontend + nginx`
- `deploy-backend.sh`：仅后端发版，更新代码并重建 `backend + nginx`
- `deploy-restart.sh`：无代码改动重启，不拉代码、不 build
- `deploy-status.sh`：查看容器状态、健康检查和最近日志
- `deploy-prod.sh`：兼容入口，当前等同于 `deploy-release.sh`
- `docker-prune.sh`：清理 Docker 构建缓存和悬挂镜像
- `backup-postgres.sh`：执行 PostgreSQL 备份

## 2. 常规全量发版

适用：

- 前端、后端都改了
- 不想判断改动面，直接按标准发版

```bash
/opt/personal-tooling/app/deploy/deploy-release.sh
```

说明：

- 会先 `git pull --ff-only`
- 会重建 `backend`、`frontend`、`nginx`
- 不会清掉 Docker build cache

如果需要顺便拉一下 `postgres` / `nginx` 远程镜像更新：

```bash
PULL_IMAGES=1 /opt/personal-tooling/app/deploy/deploy-release.sh
```

## 3. 仅前端发版

适用：

- 只改了 React / Vite 页面、样式、前端逻辑

```bash
/opt/personal-tooling/app/deploy/deploy-frontend.sh
```

说明：

- 会先 `git pull --ff-only`
- 只重建 `frontend` 和 `nginx`
- 不动 `backend` 和 `postgres`

## 4. 仅后端发版

适用：

- 只改了 Go API、任务逻辑、数据库访问

```bash
/opt/personal-tooling/app/deploy/deploy-backend.sh
```

说明：

- 会先 `git pull --ff-only`
- 只重建 `backend` 和 `nginx`
- 不动 `frontend`

## 5. 无改动重启

适用：

- 服务异常后重启
- 宿主机重启后手动拉起
- 只是想重启容器，不需要重新 build

```bash
/opt/personal-tooling/app/deploy/deploy-restart.sh
```

## 6. 状态检查

```bash
/opt/personal-tooling/app/deploy/deploy-status.sh
```

默认会输出：

- `docker compose ps`
- `/healthz`
- `/readyz`
- `nginx / backend / postgres` 最近 `20` 行日志

如果想看更多日志：

```bash
LOG_TAIL=100 /opt/personal-tooling/app/deploy/deploy-status.sh
```

## 7. 兼容入口

旧命令仍可继续用：

```bash
/opt/personal-tooling/app/deploy/deploy-prod.sh
```

但它现在只是转发到：

```bash
/opt/personal-tooling/app/deploy/deploy-release.sh
```

## 8. 磁盘清理

日常发版不再自动清理 Docker build cache。  
只有磁盘压力变大时，再手动执行：

```bash
/opt/personal-tooling/app/deploy/docker-prune.sh
```

如果还想额外清理悬挂镜像：

```bash
PRUNE_IMAGES=1 /opt/personal-tooling/app/deploy/docker-prune.sh
```

如果明确要做更激进的系统清理：

```bash
PRUNE_SYSTEM=1 /opt/personal-tooling/app/deploy/docker-prune.sh
```

## 9. 数据备份

```bash
/opt/personal-tooling/app/deploy/backup-postgres.sh
```

建议加 cron：

```cron
0 3 * * * /opt/personal-tooling/app/deploy/backup-postgres.sh >/opt/personal-tooling/logs/backup.log 2>&1
```

## 10. 回滚

最简单的回滚方式：

```bash
cd /opt/personal-tooling/app
git log --oneline
git checkout <stable-commit-or-tag>
/opt/personal-tooling/app/deploy/deploy-release.sh
```

## 11. 常用排障命令

```bash
docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml ps
docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml logs --tail=200 nginx
docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml logs --tail=200 backend
docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml logs --tail=200 postgres
docker stats
df -h
docker system df
```
