# macOS 离线镜像包发版方案

本文适用于不想在云服务器上执行 `npm install`、`npm run build`、`go build` 或 `docker build` 的场景。

目标流程：

1. 在 macOS 本机把前端、后端构建成 Linux Docker 镜像。
2. 把镜像导出成一个 `tar.gz` 离线包。
3. 上传到云服务器。
4. 服务器只执行 `docker load` 和 `docker compose up -d --pull never`。

## 1. 适用前提

- macOS 本机已安装 Docker Desktop。
- 云服务器已安装 Docker 和 Docker Compose plugin。
- 项目生产编排使用 `deploy/docker-compose.prod.yml`。
- 生产运行根目录统一使用 `/opt/personal-tooling`。
- 云服务器架构通常是 `linux/amd64`；如果是 ARM 服务器，把下面命令里的 `linux/amd64` 改成 `linux/arm64`。

服务器目录约定：

```text
/opt/personal-tooling
├── app/
│   └── deploy/
├── env/
│   ├── prod.env
│   └── .htpasswd
├── data/
│   ├── postgres/
│   └── backend/
├── logs/
└── backups/
```

## 2. macOS 本地打离线发布包

在 macOS 本机进入项目根目录：

```bash
cd /path/to/mongojson
```

生成版本号：

```bash
TAG=$(date +%Y%m%d-%H%M)
PLATFORM=linux/amd64
```

构建前后端镜像：

```bash
docker buildx build --platform "$PLATFORM" -t mongojson-backend:$TAG --load ./backend
docker buildx build --platform "$PLATFORM" -t mongojson-frontend:$TAG --load ./frontend
```

准备基础镜像。空服务器首次离线部署时需要一起带上 `postgres` 和 `nginx`，避免服务器临时拉取外网镜像：

```bash
docker pull --platform "$PLATFORM" postgres:17
docker pull --platform "$PLATFORM" nginx:1.29-alpine
```

生成离线包目录：

```bash
mkdir -p output/release-$TAG/deploy

docker save \
  mongojson-backend:$TAG \
  mongojson-frontend:$TAG \
  postgres:17 \
  nginx:1.29-alpine \
  | gzip -c > output/release-$TAG/images.tar.gz

cp deploy/docker-compose.prod.yml output/release-$TAG/deploy/
cp deploy/nginx.prod.conf output/release-$TAG/deploy/
```

生成生产环境变量样板：

```bash
cat > output/release-$TAG/prod.env <<EOF
POSTGRES_PASSWORD=replace_with_strong_password
BACKEND_IMAGE=mongojson-backend:$TAG
FRONTEND_IMAGE=mongojson-frontend:$TAG
EOF
```

打成一个可上传文件：

```bash
tar -C output -czf mongojson-release-$TAG.tar.gz release-$TAG
```

产物：

```text
mongojson-release-<TAG>.tar.gz
```

如果前端镜像构建时报 `packages/vditor-core` 找不到，检查 `frontend/Dockerfile` 是否在安装依赖前复制了本地包元信息。前端依赖里有 `@mongojson/vditor-core: file:packages/vditor-core`，构建上下文必须包含这个目录。

## 3. 上传离线包

```bash
scp mongojson-release-$TAG.tar.gz root@<server-ip>:/tmp/
```

后续服务器命令都默认在云服务器上执行。

## 4. 情况一：空的云服务器

适用：服务器还没有部署过这个项目，`/opt/personal-tooling` 不存在或为空。

### 4.1 安装系统依赖

CentOS / Rocky / AlmaLinux：

```bash
dnf install -y docker-compose-plugin httpd-tools
systemctl enable --now docker
```

Debian / Ubuntu：

```bash
apt-get update
apt-get install -y docker.io docker-compose-plugin apache2-utils
systemctl enable --now docker
```

确认：

```bash
docker version
docker compose version
```

### 4.2 创建目录并解包

```bash
mkdir -p /opt/personal-tooling/{app/deploy,env,data/postgres,data/backend,logs,backups}

cd /tmp
tar -xzf mongojson-release-*.tar.gz
RELEASE_DIR=$(find /tmp -maxdepth 1 -type d -name 'release-*' | sort | tail -1)

cp "$RELEASE_DIR"/deploy/docker-compose.prod.yml /opt/personal-tooling/app/deploy/
cp "$RELEASE_DIR"/deploy/nginx.prod.conf /opt/personal-tooling/app/deploy/
cp "$RELEASE_DIR"/prod.env /opt/personal-tooling/env/prod.env
```

编辑生产环境变量：

```bash
vi /opt/personal-tooling/env/prod.env
```

至少替换：

```bash
POSTGRES_PASSWORD=replace_with_strong_password
```

### 4.3 创建 Basic Auth 密码

```bash
htpasswd -bc /opt/personal-tooling/env/.htpasswd admin '<strong-basic-auth-password>'
```

### 4.4 加载镜像并启动

```bash
docker load -i "$RELEASE_DIR"/images.tar.gz

cd /opt/personal-tooling/app
docker compose \
  --env-file /opt/personal-tooling/env/prod.env \
  -f deploy/docker-compose.prod.yml \
  up -d --pull never
```

### 4.5 验收

```bash
cd /opt/personal-tooling/app
docker compose \
  --env-file /opt/personal-tooling/env/prod.env \
  -f deploy/docker-compose.prod.yml \
  ps

curl http://127.0.0.1/healthz
curl http://127.0.0.1/readyz
```

预期：

- `postgres` 是 `healthy`。
- `backend`、`frontend`、`nginx` 都是 `running`。
- `/healthz` 和 `/readyz` 返回成功。

## 5. 情况二：曾经使用这种方法部署过的云服务器

适用：服务器之前就是用离线镜像包部署的，已经存在：

- `/opt/personal-tooling/app/deploy/docker-compose.prod.yml`
- `/opt/personal-tooling/env/prod.env`
- `/opt/personal-tooling/env/.htpasswd`
- `/opt/personal-tooling/data/postgres`

这种情况不要覆盖数据库目录，也通常不需要重新生成 `.htpasswd`。

### 5.1 解包新版本

```bash
cd /tmp
tar -xzf mongojson-release-*.tar.gz
RELEASE_DIR=$(find /tmp -maxdepth 1 -type d -name 'release-*' | sort | tail -1)
```

### 5.2 加载新镜像

```bash
docker load -i "$RELEASE_DIR"/images.tar.gz
```

### 5.3 更新 compose 文件和镜像标签

```bash
cp "$RELEASE_DIR"/deploy/docker-compose.prod.yml /opt/personal-tooling/app/deploy/
cp "$RELEASE_DIR"/deploy/nginx.prod.conf /opt/personal-tooling/app/deploy/
```

从新包的 `prod.env` 里查看新镜像标签：

```bash
grep -E '^(BACKEND_IMAGE|FRONTEND_IMAGE)=' "$RELEASE_DIR"/prod.env
```

编辑服务器环境文件：

```bash
vi /opt/personal-tooling/env/prod.env
```

只更新这两行：

```bash
BACKEND_IMAGE=mongojson-backend:<new-tag>
FRONTEND_IMAGE=mongojson-frontend:<new-tag>
```

不要把服务器已有的 `POSTGRES_PASSWORD` 改成样板值。

### 5.4 重启到新版本

```bash
cd /opt/personal-tooling/app
docker compose \
  --env-file /opt/personal-tooling/env/prod.env \
  -f deploy/docker-compose.prod.yml \
  up -d --pull never

docker compose \
  --env-file /opt/personal-tooling/env/prod.env \
  -f deploy/docker-compose.prod.yml \
  restart nginx
```

### 5.5 验收

```bash
cd /opt/personal-tooling/app
docker compose \
  --env-file /opt/personal-tooling/env/prod.env \
  -f deploy/docker-compose.prod.yml \
  ps

curl http://127.0.0.1/healthz
curl http://127.0.0.1/readyz
```

如果要确认当前使用的镜像：

```bash
docker inspect $(docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml ps -q backend) --format '{{.Config.Image}}'
docker inspect $(docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml ps -q frontend) --format '{{.Config.Image}}'
```

## 6. 情况三：拉取过代码通过服务器自己打包发布的云服务器

适用：服务器曾经执行过类似：

```bash
git clone ...
docker compose up --build
```

或者在服务器上通过 `docker-compose.yml` 现场构建前后端镜像。

这种服务器的核心处理是：停止旧的“服务器现场构建”栈，保留数据目录，切换到 `deploy/docker-compose.prod.yml` + 离线镜像。

### 6.1 先备份数据库

如果旧服务还在运行，先备份 PostgreSQL。容器名不确定时先看：

```bash
docker ps
```

如果旧数据库是当前项目 compose 里的 `postgres` 服务，可以尝试：

```bash
cd /opt/personal-tooling/app
mkdir -p /opt/personal-tooling/backups

docker compose exec -T postgres pg_dump \
  -U postgres \
  --no-owner \
  --no-privileges \
  --clean \
  --if-exists \
  -d mongojson \
  > /opt/personal-tooling/backups/mongojson-before-offline-switch-$(date +%Y%m%d-%H%M%S).sql
```

如果旧数据库已经使用 `tooling_app` 用户，则改成：

```bash
docker compose exec -T postgres pg_dump \
  -U tooling_app \
  --no-owner \
  --no-privileges \
  --clean \
  --if-exists \
  -d mongojson \
  > /opt/personal-tooling/backups/mongojson-before-offline-switch-$(date +%Y%m%d-%H%M%S).sql
```

记录备份文件路径，后面恢复会用到：

```bash
ls -lh /opt/personal-tooling/backups/mongojson-before-offline-switch-*.sql
```

### 6.2 停止旧栈

如果旧栈是在 `/opt/personal-tooling/app/docker-compose.yml` 启动的：

```bash
cd /opt/personal-tooling/app
docker compose down
```

不要删除 volume，也不要执行 `docker compose down -v`。

如果旧栈不是这个目录启动的，先用 `docker ps` 找到容器，再回到当初启动它的目录执行对应的 `docker compose down`。

### 6.3 准备生产目录

```bash
mkdir -p /opt/personal-tooling/{app/deploy,env,data/postgres,data/backend,logs,backups}
```

如果旧数据使用 Docker named volume，而不是 `/opt/personal-tooling/data/postgres` 目录，不要直接复制 Docker volume 内部文件。按上面的 `pg_dump` 备份，并在新生产栈启动 PostgreSQL 后用 `psql` 恢复。

### 6.4 解包并加载镜像

```bash
cd /tmp
tar -xzf mongojson-release-*.tar.gz
RELEASE_DIR=$(find /tmp -maxdepth 1 -type d -name 'release-*' | sort | tail -1)

docker load -i "$RELEASE_DIR"/images.tar.gz

cp "$RELEASE_DIR"/deploy/docker-compose.prod.yml /opt/personal-tooling/app/deploy/
cp "$RELEASE_DIR"/deploy/nginx.prod.conf /opt/personal-tooling/app/deploy/
```

### 6.5 准备环境文件

如果 `/opt/personal-tooling/env/prod.env` 不存在：

```bash
cp "$RELEASE_DIR"/prod.env /opt/personal-tooling/env/prod.env
vi /opt/personal-tooling/env/prod.env
```

替换：

```bash
POSTGRES_PASSWORD=<strong-postgres-password>
```

如果 `/opt/personal-tooling/env/prod.env` 已存在，只更新镜像标签：

```bash
grep -E '^(BACKEND_IMAGE|FRONTEND_IMAGE)=' "$RELEASE_DIR"/prod.env
vi /opt/personal-tooling/env/prod.env
```

如果 `.htpasswd` 不存在：

```bash
htpasswd -bc /opt/personal-tooling/env/.htpasswd admin '<strong-basic-auth-password>'
```

### 6.6 启动 PostgreSQL 并按需恢复旧数据

如果旧服务器有需要保留的数据，先只启动新生产栈的 `postgres`：

```bash
cd /opt/personal-tooling/app
docker compose \
  --env-file /opt/personal-tooling/env/prod.env \
  -f deploy/docker-compose.prod.yml \
  up -d --pull never postgres
```

等待数据库健康：

```bash
docker compose \
  --env-file /opt/personal-tooling/env/prod.env \
  -f deploy/docker-compose.prod.yml \
  ps postgres
```

恢复旧数据：

```bash
BACKUP_SQL=/opt/personal-tooling/backups/<your-backup-file>.sql

docker compose \
  --env-file /opt/personal-tooling/env/prod.env \
  -f deploy/docker-compose.prod.yml \
  exec -T postgres \
  psql -U tooling_app -d mongojson < "$BACKUP_SQL"
```

如果这是一次全新数据部署，没有旧数据要保留，可以跳过本节的恢复命令。

### 6.7 启动离线镜像版本

```bash
cd /opt/personal-tooling/app
docker compose \
  --env-file /opt/personal-tooling/env/prod.env \
  -f deploy/docker-compose.prod.yml \
  up -d --pull never
```

### 6.8 验收并确认不再现场构建

```bash
cd /opt/personal-tooling/app
docker compose \
  --env-file /opt/personal-tooling/env/prod.env \
  -f deploy/docker-compose.prod.yml \
  ps

curl http://127.0.0.1/healthz
curl http://127.0.0.1/readyz
```

确认当前 compose 文件里没有 `build:`：

```bash
grep -n 'build:' /opt/personal-tooling/app/deploy/docker-compose.prod.yml || true
```

没有输出才表示生产发版不再依赖服务器构建。

## 7. 常用回滚

离线包发版的回滚方式是把 `prod.env` 里的镜像标签改回上一个版本，然后重新启动：

```bash
vi /opt/personal-tooling/env/prod.env

cd /opt/personal-tooling/app
docker compose \
  --env-file /opt/personal-tooling/env/prod.env \
  -f deploy/docker-compose.prod.yml \
  up -d --pull never

docker compose \
  --env-file /opt/personal-tooling/env/prod.env \
  -f deploy/docker-compose.prod.yml \
  restart nginx
```

前提是旧镜像还在服务器上。可以用下面命令查看：

```bash
docker images | grep mongojson
```

## 8. 清理旧镜像

确认新版本稳定后再清理。先看占用：

```bash
docker system df
docker images | grep mongojson
```

删除明确不用的旧版本：

```bash
docker rmi mongojson-backend:<old-tag> mongojson-frontend:<old-tag>
```

不要清理 PostgreSQL 数据目录：

```text
/opt/personal-tooling/data/postgres
```

## 9. 为什么不用现有 deploy-release.sh

现有 `deploy-release.sh` 面向“镜像仓库发布”流程，会执行：

- `git pull --ff-only`
- `docker compose pull backend frontend nginx`
- 重启服务

离线包发版时，前后端镜像已经通过 `docker load` 进入服务器本地 Docker，不应该再让服务器 `pull`。因此离线发布统一使用：

```bash
docker compose \
  --env-file /opt/personal-tooling/env/prod.env \
  -f deploy/docker-compose.prod.yml \
  up -d --pull never
```
