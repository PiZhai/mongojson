# 日常运维 Runbook

这份文档只负责**已部署完成之后**的日常操作。  
首次部署请先看 [docs/deploy-centos-stream-10.md](/Users/administrator/Documents/mongojson/docs/deploy-centos-stream-10.md)。

## 1. 脚本总览

默认脚本目录：

```bash
/opt/personal-tooling/app/deploy
```

可用脚本：

- `build-push-images.ps1`：本地构建 `linux/amd64` 前后端镜像并推送到镜像仓库
- `deploy-release.sh`：常规全量发版，更新代码，拉取前后端镜像并重启服务
- `deploy-frontend.sh`：仅前端发版，更新代码，拉取 `frontend` 镜像并重启 `frontend + nginx`
- `deploy-backend.sh`：仅后端发版，更新代码，拉取 `backend` 镜像并重启 `backend + nginx`
- `deploy-restart.sh`：无代码改动重启，不拉代码、不 build
- `deploy-status.sh`：查看容器状态、健康检查和最近日志
- `deploy-prod.sh`：兼容入口，当前等同于 `deploy-release.sh`
- `docker-prune.sh`：清理 Docker 构建缓存和悬挂镜像
- `backup-postgres.sh`：执行 PostgreSQL 备份

生产环境不在服务器上现场构建前后端镜像。发版前先在本地或 CI 构建并推送镜像，再把 `/opt/personal-tooling/env/prod.env` 里的镜像标签更新到新版本。

如果你想跳过代码拉取，只拉镜像并重启：

```bash
SKIP_PULL=1 /opt/personal-tooling/app/deploy/deploy-release.sh
```

同样适用于 `deploy-init.sh`、`deploy-frontend.sh`、`deploy-backend.sh`。

## 2. 本地构建并推送镜像

适用：每次准备发布新代码前。

Windows PowerShell：

```powershell
cd C:\Mine\projects\mongojson
.\deploy\build-push-images.ps1 `
  -Registry registry.cn-hangzhou.aliyuncs.com/your-namespace `
  -Tag 20260627-001
```

脚本会输出需要写入生产环境文件的两行：

```bash
BACKEND_IMAGE=registry.cn-hangzhou.aliyuncs.com/your-namespace/mongojson-backend:20260627-001
FRONTEND_IMAGE=registry.cn-hangzhou.aliyuncs.com/your-namespace/mongojson-frontend:20260627-001
```

如果使用阿里云 ACR，本地和服务器都需要先登录：

```bash
docker login registry.cn-hangzhou.aliyuncs.com
```

## 3. 常规全量发版

适用：

- 前端、后端都改了
- 不想判断改动面，直接按标准发版

先更新生产环境镜像标签：

```bash
vi /opt/personal-tooling/env/prod.env
```

确认包含新版本：

```bash
BACKEND_IMAGE=registry.cn-hangzhou.aliyuncs.com/your-namespace/mongojson-backend:20260627-001
FRONTEND_IMAGE=registry.cn-hangzhou.aliyuncs.com/your-namespace/mongojson-frontend:20260627-001
```

```bash
/opt/personal-tooling/app/deploy/deploy-release.sh
```

说明：

- 会先 `git pull --ff-only`
- 会 `docker compose pull backend frontend nginx`
- 会重启 `backend`、`frontend`、`nginx`
- 发版后会额外重启一次 `nginx`，刷新 upstream 容器 IP，避免旧解析导致 `502`
- 不会在服务器上执行 `npm run build` 或 `go build`

如果需要顺便拉一下 `postgres` / `nginx` 远程镜像更新：

```bash
PULL_IMAGES=1 /opt/personal-tooling/app/deploy/deploy-release.sh
```

## 4. 仅前端发版

适用：

- 只改了 React / Vite 页面、样式、前端逻辑

```bash
/opt/personal-tooling/app/deploy/deploy-frontend.sh
```

说明：

- 会先 `git pull --ff-only`
- 只拉取 `frontend` 和 `nginx` 镜像
- 只重启 `frontend` 和 `nginx`
- 发版后会额外重启一次 `nginx`，刷新 upstream 容器 IP
- 不动 `backend` 和 `postgres`

## 5. 仅后端发版

适用：

- 只改了 Go API、任务逻辑、数据库访问

```bash
/opt/personal-tooling/app/deploy/deploy-backend.sh
```

说明：

- 会先 `git pull --ff-only`
- 只拉取 `backend` 和 `nginx` 镜像
- 只重启 `backend` 和 `nginx`
- 发版后会额外重启一次 `nginx`，刷新 upstream 容器 IP
- 不动 `frontend`

## 6. 无改动重启

适用：

- 服务异常后重启
- 宿主机重启后手动拉起
- 只是想重启容器，不需要重新 build

```bash
/opt/personal-tooling/app/deploy/deploy-restart.sh
```

## 7. 状态检查

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

## 8. 兼容入口

旧命令仍可继续用：

```bash
/opt/personal-tooling/app/deploy/deploy-prod.sh
```

但它现在只是转发到：

```bash
/opt/personal-tooling/app/deploy/deploy-release.sh
```

## 9. 磁盘清理

生产机不再现场 build，通常不需要清理 Docker build cache。
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

## 10. 数据备份

```bash
/opt/personal-tooling/app/deploy/backup-postgres.sh
```

建议加 cron：

```cron
0 3 * * * /opt/personal-tooling/app/deploy/backup-postgres.sh >/opt/personal-tooling/logs/backup.log 2>&1
```

## 11. 回滚

最简单的回滚方式是把 `prod.env` 里的镜像标签改回上一个版本，然后重新发布：

```bash
vi /opt/personal-tooling/env/prod.env
/opt/personal-tooling/app/deploy/deploy-release.sh
```

## 12. 常用排障命令

```bash
docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml ps
docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml logs --tail=200 nginx
docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml logs --tail=200 backend
docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml logs --tail=200 postgres
docker stats
df -h
docker system df
```
