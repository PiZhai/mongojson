# Personal Tooling Platform 云服务器完整部署文档

本文是一份面向当前项目的完整云服务器部署文档，目标是做到：

- 只看这一份文档，就能完成部署
- 包含每一步命令
- 包含需要创建或修改的配置文件全文
- 包含启动、验证、备份、回滚和排障

适用对象：

- 当前项目：`Personal Tooling Platform`
- 服务器系统：`CentOS Stream 10`
- 当前服务器已确认：
  - Docker 已安装：`Docker version 29.4.2`
  - 磁盘可用空间约 `30G`
  - 推荐工作目录：`/opt/personal-tooling`

同时考虑到当前部署环境是国内云服务器，本文档对应的构建方案已经默认兼容以下常见问题：

- `proxy.golang.org` 访问超时
- `registry.npmjs.org` 访问慢
- `deb.debian.org` 访问慢

---

## 1. 最终部署架构

部署采用单机 Docker Compose 架构：

- `nginx`
  - HTTPS 公网入口（HTTP 只做健康检查和跳转）
  - 转发前端页面
  - 转发 `/api`
  - Basic Auth 访问控制
- `frontend`
  - React + Vite 构建后的静态页面
- `backend`
  - Go API
  - 内置任务 worker
- `postgres`
  - 主数据库

首期不部署：

- MongoDB
- 独立消息队列
- 对象存储
- 文档转换服务

这套方案适合当前项目，也适合你这台服务器的资源情况。

---

## 2. 服务器目录规划

统一采用以下目录结构：

```text
/opt/personal-tooling
├── app/                    项目代码
├── env/                    环境变量与认证文件
├── data/
│   ├── postgres/           PostgreSQL 数据
│   └── backend/            后端文件存储目录
├── backups/                数据库备份目录
└── logs/                   脚本与运维日志
```

创建目录：

```bash
mkdir -p /opt/personal-tooling/{app,env,data/postgres,data/backend,backups,logs}
```

检查结果：

```bash
ls -lah /opt/personal-tooling
```

---

## 3. 安全组与端口建议

公网只开放以下端口：

- `22/tcp`：SSH
- `80/tcp`：HTTP
- `443/tcp`：HTTPS

不要开放以下端口到公网：

- `5432/tcp`：PostgreSQL
- `8080/tcp`：Go backend

如果你使用阿里云安全组，建议策略如下：

- `22` 只允许你的固定办公 IP 或家庭 IP
- `80` 全网
- `443` 全网

---

## 4. 系统准备

### 4.1 查看系统信息

```bash
cat /etc/os-release
pwd
df -h
docker -v
docker compose version
```

### 4.2 安装必须工具

```bash
dnf install -y git docker-compose-plugin httpd-tools
```

说明：

- `git`：拉代码
- `docker-compose-plugin`：使用 `docker compose`
- `httpd-tools`：用于生成 Basic Auth 密码文件

### 4.3 可选：配置 Docker 镜像加速

如果这台服务器拉 Docker Hub 基础镜像很慢，建议同时配置镜像加速。

编辑：

```bash
mkdir -p /etc/docker
vi /etc/docker/daemon.json
```

写入：

```json
{
  "registry-mirrors": [
    "https://docker.m.daocloud.io",
    "https://dockerpull.com"
  ]
}
```

然后执行：

```bash
systemctl daemon-reload
systemctl restart docker
docker info | grep -A 5 "Registry Mirrors"
```

### 4.4 启动 Docker

```bash
systemctl enable --now docker
systemctl status docker --no-pager
```

如果看到 `active (running)`，说明 Docker 服务正常。

---

## 5. 拉取代码

进入部署目录：

```bash
cd /opt/personal-tooling/app
```

### 5.1 首次拉取

把下面的仓库地址替换成你的真实地址：

```bash
git clone https://github.com/PiZhai/mongojson.git .
```

### 5.2 如果已经存在仓库

```bash
cd /opt/personal-tooling/app
git pull
```

### 5.3 确认代码已拉到位

```bash
git status
ls -lah
find deploy docs backend frontend -maxdepth 2 -type f | head -50
```

---

## 6. 创建生产环境变量

当前生产 Compose 需要使用一个环境文件：

- 路径：`/opt/personal-tooling/env/prod.env`

### 6.1 复制样板

```bash
cp /opt/personal-tooling/app/deploy/.env.prod.example /opt/personal-tooling/env/prod.env
```

### 6.2 编辑环境文件

```bash
vi /opt/personal-tooling/env/prod.env
```

写入以下内容：

```dotenv
POSTGRES_PASSWORD=replace_with_your_strong_password
STEWARD_MANAGEMENT_AUTH_TOKEN=replace_with_random_32_character_management_token
STEWARD_PUBLIC_ORIGIN=https://steward.example.com
BACKEND_IMAGE=registry.cn-hangzhou.aliyuncs.com/your-namespace/mongojson-backend:20260627-001
FRONTEND_IMAGE=registry.cn-hangzhou.aliyuncs.com/your-namespace/mongojson-frontend:20260627-001
```

请把 `replace_with_your_strong_password` 替换为强密码，例如 20 位以上随机字符串；把 `STEWARD_MANAGEMENT_AUTH_TOKEN` 替换为至少 32 字符的独立随机值（例如 `openssl rand -hex 32`）；把 `STEWARD_PUBLIC_ORIGIN` 改成浏览器实际访问的 HTTPS 源（包含非默认端口，例如 `https://steward.example.com:8443`）。`BACKEND_IMAGE` 和 `FRONTEND_IMAGE` 替换为本地或 CI 构建并推送后的真实镜像标签。

### 6.3 检查环境文件（不输出秘密）

```bash
stat -c '%a %U:%G %n' /opt/personal-tooling/env/prod.env
awk -F= '/^(STEWARD_PUBLIC_ORIGIN|BACKEND_IMAGE|FRONTEND_IMAGE)=/{print $1"="$2} /^(POSTGRES_PASSWORD|STEWARD_MANAGEMENT_AUTH_TOKEN)=/{print $1"=<configured>"}' /opt/personal-tooling/env/prod.env
```

权限应为 `600`。不要执行 `cat prod.env`，也不要把它粘贴到工单或聊天中。

---

## 7. 创建 Basic Auth 密码文件

生产环境先不做应用内账号体系，使用 Nginx Basic Auth 保护访问。

执行：

```bash
htpasswd -bc /opt/personal-tooling/env/.htpasswd admin YourStrongBasicAuthPassword
```

参数说明：

- `admin`：登录用户名
- `YourStrongBasicAuthPassword`：你希望设置的访问密码

验证文件存在：

```bash
stat -c '%a %U:%G %n' /opt/personal-tooling/env/.htpasswd
```

### 7.1 准备浏览器信任的 TLS 证书

远程管理依赖浏览器安全上下文；公网 HTTP 会暴露认证数据，也无法可靠保护持久管理会话。因此生产配置强制 HTTPS，不能用 HTTP 作为正式入口。

将与 `STEWARD_PUBLIC_ORIGIN` 主机名匹配、且被浏览器信任的证书放到固定位置：

```bash
mkdir -p /opt/personal-tooling/env/tls
install -m 0644 /path/to/fullchain.pem /opt/personal-tooling/env/tls/fullchain.pem
install -m 0600 /path/to/privkey.pem /opt/personal-tooling/env/tls/privkey.pem
```

可以使用 Certbot、云厂商证书服务或组织内部 CA 取得证书。若使用 Certbot，续期后的 deploy hook 必须重新复制这两个文件并执行 `docker compose ... restart nginx`。自签名证书只有在客户端已安装并信任对应根证书时才适用。

---

## 8. 生产部署使用的配置文件全文

这一节直接给出完整配置内容，方便你在服务器上核对。

### 8.1 `deploy/.env.prod.example`

文件路径：

- `/opt/personal-tooling/app/deploy/.env.prod.example`

文件内容：

```dotenv
POSTGRES_PASSWORD=replace_with_strong_password
STEWARD_MANAGEMENT_AUTH_TOKEN=replace_with_random_32_character_management_token
STEWARD_PUBLIC_ORIGIN=https://steward.example.com
BACKEND_IMAGE=replace_with_backend_image
FRONTEND_IMAGE=replace_with_frontend_image
```

### 8.2 `deploy/docker-compose.prod.yml`

文件路径：

- `/opt/personal-tooling/app/deploy/docker-compose.prod.yml`

文件内容：

```yaml
services:
  postgres:
    image: postgres:17
    restart: unless-stopped
    environment:
      POSTGRES_DB: mongojson
      POSTGRES_USER: tooling_app
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD}
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U tooling_app -d mongojson"]
      interval: 5s
      timeout: 5s
      retries: 20
    volumes:
      - /opt/personal-tooling/data/postgres:/var/lib/postgresql/data

  backend:
    image: ${BACKEND_IMAGE:?set BACKEND_IMAGE}
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      HTTP_ADDR: :8080
      STEWARD_ALLOW_REMOTE_MANAGEMENT: "true"
      STEWARD_MANAGEMENT_AUTH_REQUIRED: "true"
      STEWARD_MANAGEMENT_AUTH_TOKEN: ${STEWARD_MANAGEMENT_AUTH_TOKEN:?set STEWARD_MANAGEMENT_AUTH_TOKEN}
      STEWARD_MANAGEMENT_ALLOWED_ORIGINS: ${STEWARD_PUBLIC_ORIGIN:?set STEWARD_PUBLIC_ORIGIN to the public https origin}
      DATABASE_URL: postgres://tooling_app:${POSTGRES_PASSWORD}@postgres:5432/mongojson?sslmode=disable
      STORAGE_DIR: /app/data
      FILE_RETENTION_HOURS: 24
    volumes:
      - /opt/personal-tooling/data/backend:/app/data

  frontend:
    image: ${FRONTEND_IMAGE:?set FRONTEND_IMAGE}
    restart: unless-stopped
    depends_on:
      - backend

  nginx:
    image: nginx:1.29-alpine
    restart: unless-stopped
    depends_on:
      - frontend
      - backend
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./nginx.prod.conf:/etc/nginx/nginx.conf:ro
      - /opt/personal-tooling/env/.htpasswd:/etc/nginx/conf.d/.htpasswd:ro
      - /opt/personal-tooling/env/tls:/etc/nginx/tls:ro
```

### 8.3 `backend/Dockerfile`

文件路径：

- `/opt/personal-tooling/app/backend/Dockerfile`

文件内容：

```dockerfile
FROM golang:1.26-bookworm AS build
WORKDIR /app

ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.google.cn

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/backend ./cmd/server

FROM debian:bookworm-slim
RUN sed -i 's|deb.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list.d/debian.sources \
    && sed -i 's|security.debian.org|mirrors.aliyun.com/debian-security|g' /etc/apt/sources.list.d/debian.sources \
    && apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    fonts-dejavu \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /out/backend /usr/local/bin/backend
COPY .env.example /app/.env.example

EXPOSE 8080
CMD ["backend"]
```

### 8.4 `frontend/Dockerfile`

文件路径：

- `/opt/personal-tooling/app/frontend/Dockerfile`

文件内容：

```dockerfile
FROM node:24-bookworm-slim AS build
WORKDIR /app

COPY package.json package-lock.json* ./
RUN npm config set registry https://registry.npmmirror.com \
    && npm install

COPY . .
RUN npm run build

FROM nginx:1.29-alpine
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
```

### 8.5 `deploy/nginx.prod.conf`

文件路径：

- `/opt/personal-tooling/app/deploy/nginx.prod.conf`

文件内容：

```nginx
events {}

http {
  include /etc/nginx/mime.types;
  default_type application/octet-stream;

  sendfile on;
  keepalive_timeout 65;
  server_tokens off;

  map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
  }

  upstream frontend_upstream {
    server frontend:80;
  }

  upstream backend_upstream {
    server backend:8080;
  }

  server {
    listen 80;
    server_name _;

    location = /healthz {
      proxy_pass http://backend_upstream/healthz;
      proxy_http_version 1.1;
      proxy_set_header Host $http_host;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Host $http_host;
      proxy_set_header X-Forwarded-Proto $scheme;
    }

    location = /readyz {
      proxy_pass http://backend_upstream/readyz;
      proxy_http_version 1.1;
      proxy_set_header Host $http_host;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Host $http_host;
      proxy_set_header X-Forwarded-Proto $scheme;
    }

    location / {
      return 308 https://$host$request_uri;
    }
  }

  server {
    listen 443 ssl;
    http2 on;
    server_name _;

    ssl_certificate /etc/nginx/tls/fullchain.pem;
    ssl_certificate_key /etc/nginx/tls/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_session_cache shared:StewardTLS:10m;
    ssl_session_timeout 1d;
    ssl_session_tickets off;

    client_max_body_size 64m;

    auth_basic "Private Tooling";
    auth_basic_user_file /etc/nginx/conf.d/.htpasswd;

    add_header Strict-Transport-Security "max-age=31536000" always;
    add_header X-Frame-Options SAMEORIGIN always;
    add_header X-Content-Type-Options nosniff always;
    add_header Referrer-Policy strict-origin-when-cross-origin always;

    location /api/ {
      proxy_pass http://backend_upstream;
      proxy_http_version 1.1;
      proxy_set_header Host $http_host;
      proxy_set_header X-Real-IP $remote_addr;
      proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Host $http_host;
      proxy_set_header X-Forwarded-Proto $scheme;
      proxy_set_header Upgrade $http_upgrade;
      proxy_set_header Connection $connection_upgrade;
      proxy_read_timeout 300s;
    }

    location / {
      proxy_pass http://frontend_upstream;
      proxy_http_version 1.1;
      proxy_set_header Host $http_host;
    }
  }
}
```

### 8.6 `deploy/deploy-prod.sh`

文件路径：

- `/opt/personal-tooling/app/deploy/deploy-prod.sh`

文件内容：

```bash
#!/usr/bin/env bash

set -euo pipefail

APP_DIR="${APP_DIR:-/opt/personal-tooling/app}"
ENV_FILE="${ENV_FILE:-/opt/personal-tooling/env/prod.env}"
COMPOSE_FILE="${COMPOSE_FILE:-deploy/docker-compose.prod.yml}"

cd "$APP_DIR"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "Missing env file: $ENV_FILE" >&2
  exit 1
fi

if [[ ! -f /opt/personal-tooling/env/.htpasswd ]]; then
  echo "Missing Basic Auth file: /opt/personal-tooling/env/.htpasswd" >&2
  exit 1
fi

docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" pull backend frontend nginx
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d backend frontend nginx
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" ps
```

### 8.7 `deploy/backup-postgres.sh`

文件路径：

- `/opt/personal-tooling/app/deploy/backup-postgres.sh`

文件内容：

```bash
#!/usr/bin/env bash

set -euo pipefail

APP_DIR="${APP_DIR:-/opt/personal-tooling/app}"
ENV_FILE="${ENV_FILE:-/opt/personal-tooling/env/prod.env}"
COMPOSE_FILE="${COMPOSE_FILE:-deploy/docker-compose.prod.yml}"
BACKUP_DIR="${BACKUP_DIR:-/opt/personal-tooling/backups}"
DB_NAME="${DB_NAME:-mongojson}"
DB_USER="${DB_USER:-tooling_app}"

mkdir -p "$BACKUP_DIR"

cd "$APP_DIR"

timestamp="$(date +%F-%H%M%S)"
output_file="$BACKUP_DIR/${DB_NAME}-${timestamp}.sql"

docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" exec -T postgres \
  pg_dump -U "$DB_USER" -d "$DB_NAME" > "$output_file"

gzip -f "$output_file"
find "$BACKUP_DIR" -type f -name "${DB_NAME}-*.sql.gz" -mtime +14 -delete

echo "Backup created: ${output_file}.gz"
```

---

## 9. 部署前校验

### 9.1 检查环境文件和认证文件

```bash
stat -c '%a %U:%G %n' /opt/personal-tooling/env/prod.env /opt/personal-tooling/env/.htpasswd /opt/personal-tooling/env/tls/privkey.pem
test -s /opt/personal-tooling/env/tls/fullchain.pem
test -s /opt/personal-tooling/env/tls/privkey.pem
```

### 9.2 检查 Compose 配置能否展开

```bash
cd /opt/personal-tooling/app
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml config --quiet
```

命令无输出且退出码为 `0`，说明环境变量和 Compose 文件能正常展开。不要在生产终端使用不带 `--quiet` 的 `config`，否则展开后的数据库密码和管理密钥可能进入终端记录或日志。

### 9.3 检查镜像标签

```bash
grep -E '^(BACKEND_IMAGE|FRONTEND_IMAGE)=' /opt/personal-tooling/env/prod.env
```

确认两个镜像标签都存在，并且服务器已登录对应镜像仓库。

---

## 10. 启动服务

### 10.1 给脚本增加执行权限

```bash
chmod +x /opt/personal-tooling/app/deploy/deploy-prod.sh
chmod +x /opt/personal-tooling/app/deploy/backup-postgres.sh
```

### 10.2 使用脚本部署

```bash
/opt/personal-tooling/app/deploy/deploy-prod.sh
```

### 10.3 如果你想手工执行

```bash
cd /opt/personal-tooling/app
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml pull backend frontend nginx
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml up -d backend frontend nginx
```

### 10.4 查看容器状态

```bash
cd /opt/personal-tooling/app
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml ps
```

预期：

- `postgres` 为 `healthy`
- `backend` 为 `Up`
- `frontend` 为 `Up`
- `nginx` 为 `Up`

---

## 11. 部署后的验证

### 11.1 查看日志

```bash
cd /opt/personal-tooling/app
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml logs --tail=200 nginx
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml logs --tail=200 backend
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml logs --tail=200 postgres
```

### 11.2 本机健康检查

```bash
curl -I http://127.0.0.1
curl http://127.0.0.1/healthz
curl http://127.0.0.1/readyz
```

预期：

- `curl -I http://127.0.0.1` 返回 `308 Permanent Redirect` 并跳转到 HTTPS
- `/healthz` 返回 `{"status":"ok"}`
- `/readyz` 返回 `{"status":"ready"}`

### 11.3 带认证访问首页

```bash
curl -u admin:YourStrongBasicAuthPassword -I https://steward.example.com
```

### 11.4 浏览器页面验收

在浏览器中打开：

```text
https://steward.example.com/
```

必须使用与证书及 `STEWARD_PUBLIC_ORIGIN` 相同的主机名和端口。不要使用公网 IP 的 HTTP 地址绕过 HTTPS。

登录 Basic Auth 后，至少检查以下页面：

- `/tools/json`
- `/tools/mongodb-json`
- `/tools/visualize`

至少做一遍功能冒烟：

1. JSON 格式化
2. MongoDB JSON 转义
3. MongoDB JSON 还原
4. MongoDB JSON 对比
5. MongoDB JSON 表格视图
6. MongoDB JSON Shell 视图
7. 数据可视化输入到图表展示

---

## 12. 日常更新发布

当你修改代码后，在服务器上发布新版本：

```bash
cd /opt/personal-tooling/app
git pull
/opt/personal-tooling/app/deploy/deploy-prod.sh
```

发布后再检查：

```bash
cd /opt/personal-tooling/app
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml ps
```

---

## 13. PostgreSQL 备份

### 13.1 手工执行备份

```bash
/opt/personal-tooling/app/deploy/backup-postgres.sh
```

查看备份文件：

```bash
ls -lah /opt/personal-tooling/backups
```

### 13.2 配置定时备份

编辑 root 的 crontab：

```bash
crontab -e
```

加入以下内容：

```cron
0 3 * * * /opt/personal-tooling/app/deploy/backup-postgres.sh >/opt/personal-tooling/logs/backup.log 2>&1
```

查看当前定时任务：

```bash
crontab -l
```

### 13.3 验证备份日志

```bash
tail -100 /opt/personal-tooling/logs/backup.log
```

---

## 14. 回滚方案

当前项目最简单可靠的回滚方式是 Git 回滚 + 重新部署。

### 14.1 查看提交记录

```bash
cd /opt/personal-tooling/app
git log --oneline --decorate -20
```

### 14.2 切换到旧版本

把下面的提交号换成你要回滚到的版本：

```bash
cd /opt/personal-tooling/app
git checkout <commit-or-tag>
```

### 14.3 重新部署

```bash
/opt/personal-tooling/app/deploy/deploy-prod.sh
```

### 14.4 回到主分支

```bash
cd /opt/personal-tooling/app
git checkout main
git pull
```

---

## 15. 常见排障命令

### 15.1 看容器状态

```bash
cd /opt/personal-tooling/app
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml ps
```

### 15.2 看日志

```bash
docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml logs --tail=200 nginx
docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml logs --tail=200 backend
docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml logs --tail=200 postgres
```

### 15.3 看资源占用

```bash
docker stats
df -h
docker system df
du -sh /opt/personal-tooling/data/*
```

### 15.4 重启服务

```bash
cd /opt/personal-tooling/app
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml restart
```

### 15.5 停止服务

```bash
cd /opt/personal-tooling/app
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml down
```

注意：

- `down` 不会删除绑定挂载的数据目录
- PostgreSQL 数据仍保存在 `/opt/personal-tooling/data/postgres`
- 后端上传/产物仍保存在 `/opt/personal-tooling/data/backend`

---

## 16. HTTPS 续期与上游代理

HTTPS 是远程管理的强制前提，不是可选优化。证书续期后需要原子替换 `/opt/personal-tooling/env/tls/fullchain.pem` 和 `privkey.pem`，然后重启 Nginx。若使用 CDN、SLB 或宿主机反向代理，仍应让其通过 HTTPS 连接本项目的 Nginx 443 端口；不要重新暴露容器 80 端口作为公网业务入口。代理必须保留原始 `Host`（含非默认端口）并设置正确的 `X-Forwarded-Proto` 和 `X-Forwarded-Host`。

---

## 17. 一次性上线命令清单

如果你想照着一条条执行，下面是一份从零开始的顺序版：

```bash
mkdir -p /opt/personal-tooling/{app,env,data/postgres,data/backend,backups,logs}
dnf install -y git docker-compose-plugin httpd-tools
systemctl enable --now docker

mkdir -p /etc/docker
cat >/etc/docker/daemon.json <<'EOF'
{
  "registry-mirrors": [
    "https://docker.m.daocloud.io",
    "https://dockerpull.com"
  ]
}
EOF
systemctl daemon-reload
systemctl restart docker

cd /opt/personal-tooling/app
git clone https://github.com/PiZhai/mongojson.git .

cp /opt/personal-tooling/app/deploy/.env.prod.example /opt/personal-tooling/env/prod.env
vi /opt/personal-tooling/env/prod.env

htpasswd -bc /opt/personal-tooling/env/.htpasswd admin YourStrongBasicAuthPassword

mkdir -p /opt/personal-tooling/env/tls
install -m 0644 /path/to/fullchain.pem /opt/personal-tooling/env/tls/fullchain.pem
install -m 0600 /path/to/privkey.pem /opt/personal-tooling/env/tls/privkey.pem

chmod +x /opt/personal-tooling/app/deploy/deploy-prod.sh
chmod +x /opt/personal-tooling/app/deploy/backup-postgres.sh

cd /opt/personal-tooling/app
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml config --quiet
/opt/personal-tooling/app/deploy/deploy-prod.sh

docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml ps
curl http://127.0.0.1/healthz
curl http://127.0.0.1/readyz
```

---

## 18. 当前版本的结论

对你现在这个项目和这台服务器，推荐结论很明确：

- 用 `Docker Compose` 单机部署
- 用 `PostgreSQL` 作为唯一主库
- 用 `Go backend` 做服务层
- 用 `Nginx` 做反向代理和访问控制
- 用 `Basic Auth` 做第一阶段保护
- 文件落盘到 `/opt/personal-tooling/data/backend`
- 数据库落盘到 `/opt/personal-tooling/data/postgres`

这已经是一套可以正式上线、也方便后续继续扩展的基础架构。
