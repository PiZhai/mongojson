# CentOS Stream 10 云服务器首次部署方案

本文适用于当前这台云服务器：

- OS: `CentOS Stream 10`
- Docker: `29.4.2`
- 推荐根目录：`/opt/personal-tooling`

目标是从一台空的 CentOS Stream 10 机器出发，把当前项目以 `Docker Compose + Nginx + Go backend + React frontend + PostgreSQL` 方式首次部署上线。

## 1. 部署形态

采用单机容器编排：

- `nginx`：公网入口，转发前端和 API
- `frontend`：从镜像仓库拉取的 React/Vite 构建产物镜像
- `backend`：从镜像仓库拉取的 Go API 镜像
- `postgres`：主数据库

生产机不现场编译前后端。先在本地或 CI 构建并推送镜像，生产机只执行 `pull + restart`。首次部署阶段使用**一键全量部署**，后续日常发版再区分：

- 全量发版
- 仅前端发版
- 仅后端发版
- 无改动重启

## 2. 服务器目录规划

统一目录：

```text
/opt/personal-tooling
├── app/
├── env/
├── data/
│   ├── postgres/
│   └── backend/
├── logs/
└── backups/
```

初始化目录：

```bash
mkdir -p /opt/personal-tooling/{app,env,data/postgres,data/backend,logs,backups}
```

## 3. 安全组与端口

公网建议只开放：

- `22`
- `80`
- `443`

不要开放：

- `5432`
- `8080`

## 4. 系统准备

安装依赖：

```bash
dnf install -y git docker-compose-plugin httpd-tools
systemctl enable --now docker
docker compose version
```

如果 Docker Hub 拉取偏慢，可以先配置镜像加速，再继续部署。

## 5. 拉取代码

```bash
cd /opt/personal-tooling/app
git clone https://github.com/PiZhai/mongojson.git .
```

## 6. 一键首次部署

首次部署前，先在本地构建并推送镜像：

```powershell
cd C:\Mine\projects\mongojson
.\deploy\build-push-images.ps1 `
  -Registry registry.cn-hangzhou.aliyuncs.com/your-namespace `
  -Tag 20260627-001
```

推荐直接执行：

```bash
POSTGRES_PASSWORD='<strong-postgres-password>' \
BASIC_AUTH_PASSWORD='<strong-basic-auth-password>' \
BACKEND_IMAGE='registry.cn-hangzhou.aliyuncs.com/your-namespace/mongojson-backend:20260627-001' \
FRONTEND_IMAGE='registry.cn-hangzhou.aliyuncs.com/your-namespace/mongojson-frontend:20260627-001' \
/opt/personal-tooling/app/deploy/deploy-init.sh
```

说明：

- `POSTGRES_PASSWORD`：写入 `/opt/personal-tooling/env/prod.env`
- `BASIC_AUTH_PASSWORD`：生成 `/opt/personal-tooling/env/.htpasswd`
- `BASIC_AUTH_USER` 默认是 `admin`
- `BACKEND_IMAGE` / `FRONTEND_IMAGE`：生产机要拉取的前后端镜像标签

如果要自定义 Basic Auth 用户名：

```bash
POSTGRES_PASSWORD='<strong-postgres-password>' \
BASIC_AUTH_USER='your-admin-name' \
BASIC_AUTH_PASSWORD='<strong-basic-auth-password>' \
BACKEND_IMAGE='registry.cn-hangzhou.aliyuncs.com/your-namespace/mongojson-backend:20260627-001' \
FRONTEND_IMAGE='registry.cn-hangzhou.aliyuncs.com/your-namespace/mongojson-frontend:20260627-001' \
/opt/personal-tooling/app/deploy/deploy-init.sh
```

## 7. 分步方式

如果你更希望先自己准备环境文件，再执行首次部署，也可以这样：

```bash
cp /opt/personal-tooling/app/deploy/.env.prod.example /opt/personal-tooling/env/prod.env
vi /opt/personal-tooling/env/prod.env
htpasswd -bc /opt/personal-tooling/env/.htpasswd admin <strong-password>
/opt/personal-tooling/app/deploy/deploy-init.sh
```

## 8. 首次上线验收

检查容器：

```bash
docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml ps
```

检查健康接口：

```bash
curl -I http://127.0.0.1
curl http://127.0.0.1/healthz
curl http://127.0.0.1/readyz
```

预期：

- `postgres` healthy
- `backend` running
- `frontend` running
- `nginx` running

页面至少检查：

- `/tools/json`
- `/tools/mongodb-json`
- `/tools/visualize`

功能至少跑一遍：

1. JSON 格式化
2. MongoDB JSON 转义/还原
3. MongoDB JSON 对比
4. 可视化渲染

## 9. 后续运维入口

首次部署完成后，后续不要再用“手敲长 compose 命令”作为主方式，优先用这些脚本：

```bash
/opt/personal-tooling/app/deploy/deploy-release.sh
/opt/personal-tooling/app/deploy/deploy-frontend.sh
/opt/personal-tooling/app/deploy/deploy-backend.sh
/opt/personal-tooling/app/deploy/deploy-restart.sh
/opt/personal-tooling/app/deploy/deploy-status.sh
```

完整日常运维说明见：

- [docs/deploy-runbook.md](/Users/administrator/Documents/mongojson/docs/deploy-runbook.md)
