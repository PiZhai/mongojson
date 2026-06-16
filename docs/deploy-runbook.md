# 部署执行清单

这份清单适合第一次把项目上线到当前 CentOS Stream 10 云服务器时直接照着执行。

## A. 首次上线前

1. 安全组仅开放 `22/80/443`
2. 确认 Docker 服务正常
3. 确认磁盘剩余至少 `10G+`
4. 准备域名或先用 IP 访问
5. 准备强密码：
   - PostgreSQL
   - Basic Auth

## B. 服务器初始化

```bash
mkdir -p /opt/personal-tooling/{app,env,data/postgres,data/backend,logs,backups}
dnf install -y git docker-compose-plugin httpd-tools
systemctl enable --now docker
```

## C. 拉代码

```bash
cd /opt/personal-tooling/app
git clone <your-repo-url> .
```

如果已经有代码：

```bash
cd /opt/personal-tooling/app
git pull
```

## D. 配环境

```bash
cp /opt/personal-tooling/app/deploy/.env.prod.example /opt/personal-tooling/env/prod.env
vi /opt/personal-tooling/env/prod.env
htpasswd -bc /opt/personal-tooling/env/.htpasswd admin <strong-password>
```

## E. 启动服务

```bash
chmod +x /opt/personal-tooling/app/deploy/deploy-prod.sh
/opt/personal-tooling/app/deploy/deploy-prod.sh
```

## F. 启动后检查

```bash
cd /opt/personal-tooling/app
docker compose --env-file /opt/personal-tooling/env/prod.env -f deploy/docker-compose.prod.yml ps
curl -I http://127.0.0.1
curl http://127.0.0.1/api/healthz
curl http://127.0.0.1/api/readyz
```

预期：

- `postgres` healthy
- `backend` up
- `frontend` up
- `nginx` up
- `/api/healthz` 返回 `ok`

## G. 页面验收

浏览器中至少检查：

1. `/tools/json`
2. `/tools/mongodb-json`
3. `/tools/visualize`

功能最少走一遍：

1. JSON 格式化
2. MongoDB JSON 转义/还原
3. MongoDB JSON 对比
4. 可视化渲染

## H. 日常发布

```bash
cd /opt/personal-tooling/app
git pull
/opt/personal-tooling/app/deploy/deploy-prod.sh
```

## I. 回滚思路

当前最简单的回滚策略是：

1. `git log --oneline` 找到上一稳定版本
2. `git checkout <commit-or-tag>`
3. 重新执行部署脚本

如果未来进入多人协作或更频繁发布阶段，建议补：

- Git tag 发布
- 镜像版本号
- 自动化 CI 构建

## J. 备份

```bash
chmod +x /opt/personal-tooling/app/deploy/backup-postgres.sh
/opt/personal-tooling/app/deploy/backup-postgres.sh
```

建议加 cron：

```cron
0 3 * * * /opt/personal-tooling/app/deploy/backup-postgres.sh >/opt/personal-tooling/logs/backup.log 2>&1
```

## K. 常用排障命令

```bash
docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml logs --tail=200 nginx
docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml logs --tail=200 backend
docker compose --env-file /opt/personal-tooling/env/prod.env -f /opt/personal-tooling/app/deploy/docker-compose.prod.yml logs --tail=200 postgres
docker stats
df -h
docker system df
```

