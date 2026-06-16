# CentOS Stream 10 云服务器部署方案

本文面向当前这台云服务器：

- OS: `CentOS Stream 10`
- Docker: `29.4.2`
- 可用磁盘：根分区约 `30G` 剩余
- 当前工作目录建议：`/opt/personal-tooling`

目标是把当前项目以 `Docker Compose + Nginx + Go backend + React frontend + PostgreSQL` 方式部署到云服务器，并为后续扩展预留空间。

## 1. 推荐部署形态

推荐采用单机容器编排：

- `nginx`：公网入口，转发前端和 API
- `frontend`：React/Vite 构建产物静态站点
- `backend`：Go API + 内置 worker
- `postgres`：主库

首期不额外拆分：

- 不单独上 MongoDB
- 不单独拆任务队列
- 不单独上对象存储

这是当前最适合这台机器的方案，原因很直接：

- 40G 磁盘足够支撑首期单人使用
- Docker 已安装，可直接进入部署阶段
- 当前项目已经内建 `docker-compose.yml`、`backend/Dockerfile`、`frontend/Dockerfile`、`deploy/nginx.conf`
- Go 单体 + PostgreSQL 的运维复杂度明显低于再拆更多服务

## 2. 服务器目录规划

建议在服务器上统一使用：

```text
/opt/personal-tooling
├── app/                项目仓库
├── env/                环境变量文件
├── data/
│   ├── postgres/       PostgreSQL 数据目录
│   └── backend/        上传文件、任务产物、临时文件
├── logs/               预留日志目录（可选）
└── backups/            数据库备份目录
```

推荐初始化命令：

```bash
mkdir -p /opt/personal-tooling/{app,env,data/postgres,data/backend,logs,backups}
```

## 3. 开放端口与网络建议

建议只对公网开放：

- `80/tcp`
- `443/tcp`

不建议直接开放：

- `5432` PostgreSQL
- `8080` backend

即使在 Docker Compose 中保留内部端口，也不要让数据库和后端裸露在公网。

如果使用阿里云安全组，建议仅允许：

- `22`：你的固定管理 IP
- `80`：公网访问
- `443`：公网访问

## 4. 系统准备

### 4.1 安装 Git 和 Compose 插件

先确认 Compose 是否可用：

```bash
docker compose version
```

如果没有，再安装：

```bash
dnf install -y git docker-compose-plugin
```

### 4.2 启动 Docker 服务

```bash
systemctl enable --now docker
systemctl status docker
```

## 5. 拉取项目

```bash
cd /opt/personal-tooling/app
git clone <your-repo-url> .
```

如果已经有仓库：

```bash
cd /opt/personal-tooling/app
git pull
```

## 6. 生产环境变量

建议把生产 Compose 所需变量单独放在：

`/opt/personal-tooling/env/prod.env`

可以直接参考仓库里的样板：

`deploy/.env.prod.example`

示例：

```dotenv
POSTGRES_PASSWORD=replace_with_strong_password
```

生产环境注意事项：

- PostgreSQL 用户不要继续使用 `postgres`
- 密码使用强随机值
- 当前生产 Compose 只要求先提供 `POSTGRES_PASSWORD`
- backend 其他参数已经直接写在 Compose 中，首期够用

## 7. PostgreSQL 初始化建议

建议生产使用独立业务账号：

- DB: `mongojson`
- User: `tooling_app`
- Password: 强密码

如果通过容器初始化，可直接在 Compose 环境变量里写入：

- `POSTGRES_DB=mongojson`
- `POSTGRES_USER=tooling_app`
- `POSTGRES_PASSWORD=<strong-password>`

## 8. 推荐的生产 Compose 方案

当前仓库已有开发/通用 Compose，可另外准备一份生产文件，例如：

- `deploy/docker-compose.prod.yml`

核心原则：

- `postgres` 和 `backend` 不对公网暴露端口
- 只暴露 `nginx:80/443`
- 数据卷显式挂载到 `/opt/personal-tooling/data`
- 使用 `restart: unless-stopped`

建议启动方式：

```bash
cd /opt/personal-tooling/app
cp deploy/.env.prod.example /opt/personal-tooling/env/prod.env
vi /opt/personal-tooling/env/prod.env
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml up -d --build
```

## 9. Nginx 生产建议

当前仓库的 `deploy/nginx.conf` 可以作为起点，但生产建议补三件事：

1. 访问控制
2. TLS
3. 基础安全头

### 9.1 第一阶段访问保护

如果暂时是个人工具平台，建议先上 Basic Auth：

```bash
dnf install -y httpd-tools
htpasswd -bc /opt/personal-tooling/env/.htpasswd admin <strong-password>
```

然后在 Nginx 中给站点加：

```nginx
auth_basic "Private Tooling";
auth_basic_user_file /etc/nginx/conf.d/.htpasswd;
```

### 9.2 HTTPS

如果已有域名，建议用 `certbot` 或反向代理统一做 TLS。

首期有两种可行方式：

1. Nginx 容器内挂载证书
2. 宿主机已有证书，再挂载到容器

如果暂时没有域名，先用 `80` 跑通，再补 `443`。

## 10. 部署步骤

推荐上线顺序：

1. 准备目录
2. 拉取代码
3. 写生产环境变量
4. 调整生产 Compose
5. 调整 Nginx 配置
6. 启动容器
7. 检查健康状态
8. 验证前端、API、上传/任务链路

命令示例：

```bash
cd /opt/personal-tooling/app
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml up -d --build
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml ps
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml logs -f nginx
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml logs -f backend
```

## 11. 上线后的验收清单

### 11.1 容器状态

```bash
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml ps
```

预期：

- `postgres` healthy
- `backend` running
- `frontend` running
- `nginx` running

### 11.2 健康检查

```bash
curl -I http://127.0.0.1
curl http://127.0.0.1/api/healthz
curl http://127.0.0.1/api/readyz
```

如果 Nginx 仍按当前仓库配置转发，则也可测试：

```bash
curl http://127.0.0.1/api/healthz
```

### 11.3 页面访问

检查：

- `/tools/json`
- `/tools/mongodb-json`
- `/tools/visualize`

### 11.4 功能回归

至少验证：

- JSON 格式化与校验
- MongoDB JSON 的格式化、对比、表格、Shell、转义/还原
- 数据可视化输入到图表渲染

## 12. 日志与排障

常用命令：

```bash
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml logs --tail=200 nginx
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml logs --tail=200 backend
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml logs --tail=200 postgres
```

看容器资源：

```bash
docker stats
```

看磁盘：

```bash
df -h
du -sh /opt/personal-tooling/data/*
docker system df
```

## 13. 备份策略

至少做 PostgreSQL 备份。

建议每天一次逻辑备份：

```bash
docker exec -t personal-tooling-postgres-1 pg_dump -U tooling_app -d mongojson > /opt/personal-tooling/backups/mongojson-$(date +%F).sql
```

建议保留：

- 最近 7 天每日备份
- 最近 4 周每周备份

如果后续文件产物变重要，再补 `/opt/personal-tooling/data/backend` 的定期打包。

## 14. 资源评估

按照当前功能范围，这台机器首期可用，但要注意边界：

- CPU/内存未知，若是 `2C2G` 或 `2C4G`，对首期个人工具站通常够用
- 磁盘剩余 `30G` 足以支撑当前版本
- 若后续加入大文件文档转换、Playwright/LibreOffice、批量任务，资源压力会明显上升

因此当前建议：

- 先不上文档转换
- 先控制文件留存时间
- 先不开放多人使用

## 15. 最适合当前项目的结论

对你现在这个项目，最适配的部署方向是：

- 前后端继续分离
- 用 Go 单体后端
- PostgreSQL 作为唯一主库
- Nginx 做公网入口和访问控制
- Docker Compose 做单机部署

这比“宿主机直接安装 Go/Node/Postgres/Nginx 混跑”更稳，也比“一开始就上 K8s 或拆多服务”更合适。

下一步最值得直接落地的是两件事：

1. 补一份生产用 `docker-compose.prod.yml`
2. 补一份带 Basic Auth 的 `nginx.prod.conf`
