# Store Server

公网鉴权与文件服务的 Go 单进程后端。用户直连本服务注册登录；应用后端拉 JWKS 做本地 RS256 验签，再查同机 Redis 黑名单。

## 技术栈

- Go + Gin
- PostgreSQL（用户、会话、文件元数据）
- Redis（Access Token JTI 黑名单 `auth:bl:{jti}`，注册登录刷新限流）
- MinIO（对象存储；分片预签名对客户端使用对外地址）

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

分片 PUT 必须打到 `MINIO_PUBLIC_ENDPOINT` 指向的客户端可达地址，格式为 `host:port`，不含协议。默认部署将 MinIO 绑定 `0.0.0.0:9000`。防火墙或宝塔安全组需要同时放行应用端口 `HOST_PORT` 与 MinIO 端口 `MINIO_HOST_PORT`，否则分片上传失败。

Redis 只发布到宿主机回环 `127.0.0.1:${REDIS_HOST_PORT:-6379}`，带 `REDIS_PASSWORD`。不要把 Redis 绑到 `0.0.0.0`。

## 应用后端验签

同机业务进程按以下顺序校验 Access Token：

1. `GET /v1/auth/.well-known/jwks.json` 拉取当前公钥，`kid` 由 RSA 公钥稳定派生。
2. 使用该公钥在本地做 RS256 验签。
3. 用回环 Redis 与密码连接，查询 `auth:bl:{jti}`；键存在则拒绝该 Access。

Compose 内应用走容器网络 `REDIS_URL`。同机其他进程使用 `redis://:${REDIS_PASSWORD}@127.0.0.1:${REDIS_HOST_PORT}/0`。

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `HOST_PORT` | 宿主机对外端口，防火墙需放行 | `8080` |
| `HTTP_PORT` | 容器内 HTTP 监听端口 | `8080` |
| `POSTGRES_USER` | PostgreSQL 用户 | `store` |
| `POSTGRES_PASSWORD` | PostgreSQL 密码 | `store` |
| `POSTGRES_DB` | PostgreSQL 数据库 | `store` |
| `DATABASE_URL` | PostgreSQL 连接串 | 必填 |
| `REDIS_HOST_PORT` | Redis 宿主机回环端口 | `6379` |
| `REDIS_PASSWORD` | Redis 密码 | `store` |
| `REDIS_URL` | Redis 连接串 | 必填 |
| `MINIO_ENDPOINT` | 容器内 MinIO 地址 | `minio:9000` |
| `MINIO_PUBLIC_ENDPOINT` | 客户端访问的 MinIO 地址，`host:port` 不含协议 | 空则预签名使用对内地址 |
| `MINIO_HOST_BIND` | MinIO 宿主机绑定地址 | `0.0.0.0` |
| `MINIO_HOST_PORT` | MinIO 宿主机端口，防火墙需放行 | `9000` |
| `MINIO_ACCESS_KEY` | MinIO Access Key | `minioadmin` |
| `MINIO_SECRET_KEY` | MinIO Secret Key | `minioadmin` |
| `MINIO_BUCKET` | 默认 Bucket 名称 | `store` |
| `MINIO_USE_SSL` | 预签名对外入口是否使用 TLS | `false` |
| `RATE_LIMIT_REGISTER` | 每客户端 IP 每 60 秒注册上限 | `5` |
| `RATE_LIMIT_LOGIN` | 每客户端 IP 每 60 秒登录上限 | `10` |
| `RATE_LIMIT_REFRESH` | 每客户端 IP 每 60 秒刷新上限 | `30` |
| `JWT_PRIVATE_KEY` | RS256 私钥 PEM，留空则开发环境自动生成 | 空 |
| `JWT_PUBLIC_KEY` | RS256 公钥 PEM | 空 |
| `JWT_PRIVATE_KEY_FILE` | RS256 私钥文件路径 | 空 |
| `JWT_PUBLIC_KEY_FILE` | RS256 公钥文件路径 | 空 |
| `MIGRATIONS_PATH` | 迁移文件目录 | `migrations` |

## 鉴权 API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/auth/register` | 用户注册，按客户端 IP 限流 |
| POST | `/v1/auth/login` | 登录，返回 Access 与 Refresh，按客户端 IP 限流 |
| POST | `/v1/auth/refresh` | 刷新 Access，Refresh 不轮换，按客户端 IP 限流 |
| POST | `/v1/auth/logout` | Bearer Access；拉黑当前 `jti` 并删除对应 session。请求体 `refresh_token` 可选 |
| POST | `/v1/auth/logout-all` | Bearer Access；删除该用户全部 session 并拉黑当前 Access |
| GET | `/v1/auth/me` | 获取当前用户信息，需 Bearer Token |
| GET | `/v1/auth/.well-known/jwks.json` | JWKS 公钥 |

超出限流返回 `429`，错误码 `RATE_LIMITED`。文件接口不限流。

错误响应统一格式：

```json
{
  "error": {
    "code": "AUTH_INVALID_TOKEN",
    "message": "access token is invalid or revoked"
  }
}
```

## 文件 API

文件元数据含 `status`：`pending` 表示分片尚未 complete，`ready` 表示可申请播放凭证。属主可读 pending 元数据；对 pending 文件签发 file-access JWT 或 GET content 返回 `409 FILE_NOT_READY`。跨用户一律 `403 FILE_FORBIDDEN`。

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/files` | 小于 10MB 直传，创建即为 `ready` |
| POST | `/v1/files/uploads` | 发起分片，插入 `pending`，预签名 host 等于 `MINIO_PUBLIC_ENDPOINT` |
| POST | `/v1/files/uploads/:upload_id/complete` | 完成分片，状态改为 `ready`，`size_bytes` 回写对象实际大小 |
| DELETE | `/v1/files/uploads/:upload_id` | 中止分片并删除文件记录 |
| GET | `/v1/files/:file_id` | 属主读取元数据，含 `status` |
| POST | `/v1/files/:file_id/access-token` | 签发 5 分钟 file-access JWT，仅 `ready` |
| GET | `/v1/files/:file_id/content` | Range 拉流，需 file-access JWT |
| DELETE | `/v1/files/:file_id` | 删除对象与记录 |

OpenAPI 由 handler 注解生成，见 `docs/swagger`。

## 本地开发

不通过 Docker 运行应用时，需自行提供 PostgreSQL 与 Redis，并设置对应环境变量：

```bash
go run ./cmd/server
```

## 测试

集成测试使用 testcontainers 拉起 PostgreSQL、Redis 与 MinIO。在 Linux CI 中直接运行即可；本地若 testcontainers 不可用，可先启动依赖服务并设置环境变量：

```bash
docker compose up -d postgres redis minio
export TEST_DATABASE_URL=postgres://store:store@localhost:5432/store?sslmode=disable
export TEST_REDIS_URL=redis://:store@127.0.0.1:6379/0
go test ./test/integration/... -v -count=1
```

对已启动的编排做登出契约探测：

```bash
go test ./test/live/... -v -count=1
```

`test/live` 默认请求 `http://127.0.0.1:8080`，可用 `LIVE_API_BASE` 覆盖。服务不可达时跳过。

## 数据库迁移

应用启动时自动执行 `migrations/` 下的 golang-migrate 迁移。
