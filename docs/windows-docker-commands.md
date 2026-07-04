# Windows Docker 启动、重启与发版命令

这份文档用于在 Windows 本机随时查看 `mongojson` 项目的 Docker 操作命令。

默认项目目录：

```powershell
C:\Mine\projects\custom_tools\mongojson
```

默认端口：

| 服务 | 容器内端口 | Windows 宿主机端口 | 访问地址 |
| --- | ---: | ---: | --- |
| nginx 前端入口 | 80 | 80 | `http://127.0.0.1/` |
| backend 直连 | 8080 | 18080 | `http://127.0.0.1:18080/` |
| postgres | 5432 | 5432 | `127.0.0.1:5432` |

健康检查：

```powershell
Invoke-WebRequest -Uri "http://127.0.0.1:18080/healthz" -UseBasicParsing
```

说明：

- 前端页面统一从 nginx 入口访问，默认是 `http://127.0.0.1/`。
- 后端容器内部仍监听 `8080`，但 Windows 宿主机默认映射到 `18080`，避免和本机已有 `8080` 服务冲突。
- 后端根路径 `/` 返回 `404` 是正常现象；健康检查请访问 `/healthz`。
- `-NoBuild` 只是启动已有旧镜像，不会把最新代码发进去。代码改动要生效，必须重新 build 对应服务。

## 1. 进入项目目录

```powershell
cd C:\Mine\projects\custom_tools\mongojson
```

## 2. 快速选择

| 场景 | 使用命令 |
| --- | --- |
| 从 0 构建并启动全部服务 | `powershell -ExecutionPolicy Bypass -File .\deploy\deploy-docker.ps1` |
| 前后端都有修改，本机完整发版 | `powershell -ExecutionPolicy Bypass -File .\deploy\deploy-docker.ps1` |
| 只有前端有修改 | 看第 4 节 |
| 只有后端有修改 | 看第 5 节 |
| 只是重启旧镜像 | `powershell -ExecutionPolicy Bypass -File .\deploy\deploy-docker.ps1 -NoBuild` |
| 80 端口被占用 | `powershell -ExecutionPolicy Bypass -File .\deploy\deploy-docker.ps1 -FrontendPort 8088` |

## 3. 从 0 构建并部署全部服务

适用场景：

- 第一次在本机部署
- 容器不存在
- 前后端都有修改
- 不想判断改动面，直接重建全部镜像

命令：

```powershell
cd C:\Mine\projects\custom_tools\mongojson
powershell -ExecutionPolicy Bypass -File .\deploy\deploy-docker.ps1
```

脚本等价于：

```powershell
docker compose -f .\docker-compose.yml up -d --remove-orphans --build
```

发版后检查：

```powershell
docker compose -f .\docker-compose.yml ps
Invoke-WebRequest -Uri "http://127.0.0.1/" -UseBasicParsing
Invoke-WebRequest -Uri "http://127.0.0.1:18080/healthz" -UseBasicParsing
```

如果前端返回 `200`，后端 `/healthz` 返回 `{"status":"ok"}`，说明本机 Docker 发版可用。

## 4. 只有前端有修改如何发版

适用场景：

- 只改了 `frontend/` 下面的页面、样式、组件、前端依赖等
- 后端代码没有修改
- 数据库不需要动

命令：

```powershell
cd C:\Mine\projects\custom_tools\mongojson
docker compose -f .\docker-compose.yml build frontend
docker compose -f .\docker-compose.yml up -d --no-deps frontend
docker compose -f .\docker-compose.yml restart nginx
```

如果之前用过非默认端口，先在同一个 PowerShell 会话里设置端口变量，例如：

```powershell
$env:FRONTEND_HOST_PORT = "8088"
$env:BACKEND_HOST_PORT = "18081"
$env:POSTGRES_HOST_PORT = "15432"
```

检查：

```powershell
docker compose -f .\docker-compose.yml ps
Invoke-WebRequest -Uri "http://127.0.0.1/" -UseBasicParsing
```

如果怀疑前端缓存导致没有更新，可以强制无缓存构建：

```powershell
docker compose -f .\docker-compose.yml build --no-cache frontend
docker compose -f .\docker-compose.yml up -d --no-deps frontend
docker compose -f .\docker-compose.yml restart nginx
```

说明：

- `frontend` 容器本身也是 nginx，用来承载 Vite 构建后的静态文件。
- 外层 `nginx` 容器负责统一入口和 `/api/` 代理。
- 重建 `frontend` 后建议重启外层 `nginx`，避免 nginx 仍持有旧 upstream 解析。

## 5. 只有后端有修改如何发版

适用场景：

- 只改了 `backend/` 下面的 Go 代码、后端 Dockerfile、后端配置等
- 前端代码没有修改

命令：

```powershell
cd C:\Mine\projects\custom_tools\mongojson
docker compose -f .\docker-compose.yml build backend
docker compose -f .\docker-compose.yml up -d --no-deps backend
docker compose -f .\docker-compose.yml restart nginx
```

如果之前用过非默认端口，先在同一个 PowerShell 会话里设置端口变量，例如：

```powershell
$env:FRONTEND_HOST_PORT = "8088"
$env:BACKEND_HOST_PORT = "18081"
$env:POSTGRES_HOST_PORT = "15432"
```

检查：

```powershell
docker compose -f .\docker-compose.yml ps
Invoke-WebRequest -Uri "http://127.0.0.1:18080/healthz" -UseBasicParsing
```

如果后端构建缓存异常，可以强制无缓存构建：

```powershell
docker compose -f .\docker-compose.yml build --no-cache backend
docker compose -f .\docker-compose.yml up -d --no-deps backend
docker compose -f .\docker-compose.yml restart nginx
```

说明：

- `--no-deps backend` 表示只替换后端容器，不重建 postgres。
- 如果 postgres 没有运行，先执行：

```powershell
docker compose -f .\docker-compose.yml up -d postgres
```

## 6. 前后端都有修改如何发版

推荐直接使用完整发版脚本：

```powershell
cd C:\Mine\projects\custom_tools\mongojson
powershell -ExecutionPolicy Bypass -File .\deploy\deploy-docker.ps1
```

如果想手动分步执行：

```powershell
cd C:\Mine\projects\custom_tools\mongojson
docker compose -f .\docker-compose.yml build frontend backend
docker compose -f .\docker-compose.yml up -d --remove-orphans frontend backend nginx
```

检查：

```powershell
docker compose -f .\docker-compose.yml ps
Invoke-WebRequest -Uri "http://127.0.0.1/" -UseBasicParsing
Invoke-WebRequest -Uri "http://127.0.0.1:18080/healthz" -UseBasicParsing
```

## 7. 只启动旧镜像，不发版

适用场景：

- Docker Hub 网络不稳定
- 只是把已有容器重新拉起来
- 不需要让最新代码生效

```powershell
cd C:\Mine\projects\custom_tools\mongojson
powershell -ExecutionPolicy Bypass -File .\deploy\deploy-docker.ps1 -NoBuild
```

注意：

- `-NoBuild` 不会重新构建镜像。
- 如果你刚改了前端或后端代码，使用 `-NoBuild` 后页面仍然可能是旧版本。

## 8. 端口号如何设置

### 8.1 用脚本参数设置端口

前端入口改为 `8088`：

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\deploy-docker.ps1 -FrontendPort 8088
```

访问：

```text
http://127.0.0.1:8088/
```

后端直连改为 `18081`：

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\deploy-docker.ps1 -BackendPort 18081
```

健康检查：

```text
http://127.0.0.1:18081/healthz
```

PostgreSQL 宿主机端口改为 `15432`：

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\deploy-docker.ps1 -PostgresPort 15432
```

三个端口一起指定：

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\deploy-docker.ps1 `
  -FrontendPort 8088 `
  -BackendPort 18081 `
  -PostgresPort 15432
```

### 8.2 手动 docker compose 命令设置端口

如果不用脚本，而是直接执行 `docker compose`，需要在当前 PowerShell 会话里设置环境变量：

```powershell
$env:FRONTEND_HOST_PORT = "8088"
$env:BACKEND_HOST_PORT = "18081"
$env:POSTGRES_HOST_PORT = "15432"
docker compose -f .\docker-compose.yml up -d --remove-orphans --build
```

访问：

```text
前端：http://127.0.0.1:8088/
后端：http://127.0.0.1:18081/healthz
PostgreSQL：127.0.0.1:15432
```

### 8.3 查看当前实际映射端口

```powershell
docker compose -f .\docker-compose.yml ps
docker compose -f .\docker-compose.yml port nginx 80
docker compose -f .\docker-compose.yml port backend 8080
docker compose -f .\docker-compose.yml port postgres 5432
```

## 9. 重启服务

重启全部服务：

```powershell
docker compose -f .\docker-compose.yml restart
```

只重启前端相关服务：

```powershell
docker compose -f .\docker-compose.yml restart frontend nginx
```

只重启后端：

```powershell
docker compose -f .\docker-compose.yml restart backend nginx
```

只重启数据库：

```powershell
docker compose -f .\docker-compose.yml restart postgres
```

## 10. 查看状态和日志

查看容器状态：

```powershell
docker compose -f .\docker-compose.yml ps
```

查看全部服务日志：

```powershell
docker compose -f .\docker-compose.yml logs --tail 200
```

持续查看日志：

```powershell
docker compose -f .\docker-compose.yml logs -f
```

查看指定服务日志：

```powershell
docker compose -f .\docker-compose.yml logs --tail 200 backend
docker compose -f .\docker-compose.yml logs --tail 200 frontend
docker compose -f .\docker-compose.yml logs --tail 200 nginx
docker compose -f .\docker-compose.yml logs --tail 200 postgres
```

## 11. 停止服务

停止并保留容器、网络和数据卷：

```powershell
docker compose -f .\docker-compose.yml stop
```

停止并删除容器、网络，保留数据卷：

```powershell
docker compose -f .\docker-compose.yml down
```

谨慎操作：删除容器、网络和数据卷，会清空本机 PostgreSQL 数据：

```powershell
docker compose -f .\docker-compose.yml down -v
```

## 12. 常见问题

### 12.1 `127.0.0.1 拒绝了我们的连接请求`

先看容器是否启动：

```powershell
docker compose -f .\docker-compose.yml ps
```

如果没有 `mongojson-nginx-1`，前端入口就不会可用。

再检查 `80` 端口是否被其他程序占用：

```powershell
netstat -ano | findstr ":80"
```

如果 `80` 被占用，使用自定义前端端口：

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\deploy-docker.ps1 -FrontendPort 8088
```

### 12.2 `Bind for 0.0.0.0:8080 failed: port is already allocated`

旧配置会让后端占用 Windows 宿主机 `8080`，容易和已有服务冲突。

当前默认配置已经改为：

```text
Windows 18080 -> backend 容器 8080
```

如果仍然冲突，改端口启动：

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\deploy-docker.ps1 -BackendPort 18081
```

### 12.3 `registry-1.docker.io ... EOF`

这是 Docker Hub 网络中断或镜像元数据解析失败，不是项目代码错误。

典型报错：

```text
failed to resolve source metadata for docker.io/library/node:24-bookworm-slim
failed to resolve source metadata for docker.io/library/golang:1.26-bookworm
failed to do request: Head "https://registry-1.docker.io/..." EOF
```

原因：

- 前端镜像依赖 `node:24-bookworm-slim`
- 后端镜像依赖 `golang:1.26-bookworm`
- 本机没有这些基础镜像缓存时，Docker 必须访问 Docker Hub
- 网络中断后就会在 build 阶段失败

如果只是启动旧镜像：

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\deploy-docker.ps1 -NoBuild
```

如果必须让最新代码生效，等网络恢复后先拉基础镜像：

```powershell
docker pull node:24-bookworm-slim
docker pull golang:1.26-bookworm
docker pull debian:bookworm-slim
docker pull nginx:1.29-alpine
docker pull postgres:17
```

然后重新发版：

```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\deploy-docker.ps1
```

## 13. 推送生产镜像

如果要从 Windows 构建并推送生产镜像，使用：

```powershell
cd C:\Mine\projects\custom_tools\mongojson
.\deploy\build-push-images.ps1 `
  -Registry registry.cn-hangzhou.aliyuncs.com/your-namespace `
  -Tag 20260704-001
```

脚本会输出需要写入生产环境 `/opt/personal-tooling/env/prod.env` 的镜像地址：

```bash
BACKEND_IMAGE=registry.cn-hangzhou.aliyuncs.com/your-namespace/mongojson-backend:20260704-001
FRONTEND_IMAGE=registry.cn-hangzhou.aliyuncs.com/your-namespace/mongojson-frontend:20260704-001
```

生产服务器发版命令仍以 [日常运维 Runbook](./deploy-runbook.md) 为准。
