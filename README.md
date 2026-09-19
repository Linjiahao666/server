# Store Server

公网鉴权与文件服务的 Go 单进程后端。当前已实现完整鉴权模块，文件模块将在后续迭代交付。

## 技术栈

- Go + Gin
- PostgreSQL（用户与会话）
- Redis（Access Token JTI 黑名单）
- MinIO（对象存储，Compose 已就绪）

## 快速启动

Linux 服务器一行安装，脚本会拉取仓库、引导配置并启动 Docker 编排：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/Linjiahao666/server/master/scripts/install.sh)
```

本地开发复制环境变量模板后启动：

```bash
cp .env.example .env
docker compose up --build
```

服务默认监听 `http://localhost:8080`。

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `HOST_PORT` | 宿主机对外端口 | `8080` |
| `HTTP_PORT` | 容器内 HTTP 监听端口 | `8080` |
| `POSTGRES_USER` | PostgreSQL 用户 | `store` |
| `POSTGRES_PASSWORD` | PostgreSQL 密码 | `store` |
| `POSTGRES_DB` | PostgreSQL 数据库 | `store` |
| `DATABASE_URL` | PostgreSQL 连接串 | 必填 |
| `REDIS_PASSWORD` | Redis 密码 | `store` |
| `REDIS_URL` | Redis 连接串 | 必填 |
| `MINIO_ENDPOINT` | MinIO 地址 | `minio:9000` |
| `MINIO_ACCESS_KEY` | MinIO Access Key | `minioadmin` |
| `MINIO_SECRET_KEY` | MinIO Secret Key | `minioadmin` |
| `MINIO_BUCKET` | 默认 Bucket 名称 | `store` |
| `MINIO_USE_SSL` | 是否启用 TLS | `false` |
| `JWT_PRIVATE_KEY` | RS256 私钥 PEM，留空则开发环境自动生成 | 空 |
| `JWT_PUBLIC_KEY` | RS256 公钥 PEM | 空 |
| `JWT_PRIVATE_KEY_FILE` | RS256 私钥文件路径 | 空 |
| `JWT_PUBLIC_KEY_FILE` | RS256 公钥文件路径 | 空 |
| `MIGRATIONS_PATH` | 迁移文件目录 | `migrations` |

## 鉴权 API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/auth/register` | 用户注册 |
| POST | `/v1/auth/login` | 登录，返回 Access + Refresh Token |
| POST | `/v1/auth/refresh` | 刷新 Access Token |
| POST | `/v1/auth/logout` | 登出，吊销 Access JTI 并失效 Refresh |
| GET | `/v1/auth/me` | 获取当前用户信息，需 Bearer Token |
| GET | `/v1/auth/.well-known/jwks.json` | JWKS 公钥 |

错误响应统一格式：

```json
{
  "error": {
    "code": "AUTH_INVALID_TOKEN",
    "message": "access token is invalid or revoked"
  }
}
```

## 本地开发

不通过 Docker 运行应用时，需自行提供 PostgreSQL 与 Redis，并设置对应环境变量：

```bash
go run ./cmd/server
```

## 测试

集成测试使用 testcontainers 拉起 PostgreSQL 与 Redis。在 Linux CI 中直接运行即可；本地若 testcontainers 不可用，可先启动依赖服务并设置环境变量：

```bash
docker compose up -d postgres redis
export TEST_DATABASE_URL=postgres://store:store@localhost:5432/store?sslmode=disable
export TEST_REDIS_URL=redis://:store@localhost:6379/0
go test ./test/integration/... -v -count=1
```

## 数据库迁移

应用启动时自动执行 `migrations/` 下的 golang-migrate 迁移，当前包含 `users` 与 `sessions` 表。
