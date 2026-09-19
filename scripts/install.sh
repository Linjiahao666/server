#!/usr/bin/env bash
set -euo pipefail

APP_REPO="https://github.com/Linjiahao666/server.git"
SECRETS_REPO="Linjiahao666/server-secrets"

if [[ ! -t 0 && -e /dev/tty ]]; then
  exec </dev/tty
fi

say() {
  printf '%s\n' "$*"
}

die() {
  printf '错误：%s\n' "$*" >&2
  exit 1
}

prompt() {
  local message="$1"
  local default="${2:-}"
  local reply=""
  if [[ -n "$default" ]]; then
    printf '%s [%s]: ' "$message" "$default"
  else
    printf '%s: ' "$message"
  fi
  read -r reply || true
  if [[ -z "$reply" ]]; then
    printf '%s' "$default"
  else
    printf '%s' "$reply"
  fi
}

confirm() {
  local message="$1"
  local default="${2:-y}"
  local hint="y/N"
  if [[ "$default" == "y" ]]; then
    hint="Y/n"
  fi
  local reply
  reply="$(prompt "$message ($hint)" "$default")"
  [[ "$reply" == "y" || "$reply" == "Y" ]]
}

run_root() {
  if [[ "$(id -u)" -eq 0 ]]; then
    "$@"
  else
    sudo "$@"
  fi
}

docker_bin() {
  if docker info >/dev/null 2>&1; then
    docker "$@"
  else
    run_root docker "$@"
  fi
}

compose() {
  docker_bin compose "$@"
}

need_bin() {
  local name="$1"
  command -v "$name" >/dev/null 2>&1
}

install_packages() {
  local packages=("$@")
  if need_bin apt-get; then
    run_root apt-get update -y
    run_root apt-get install -y "${packages[@]}"
  elif need_bin dnf; then
    run_root dnf install -y "${packages[@]}"
  elif need_bin yum; then
    run_root yum install -y "${packages[@]}"
  else
    die "无法自动安装 ${packages[*]}，请先手动安装后再运行本脚本。"
  fi
}

ensure_tools() {
  local missing=()
  need_bin git || missing+=(git)
  need_bin curl || missing+=(curl)
  need_bin openssl || missing+=(openssl)
  if [[ ${#missing[@]} -gt 0 ]]; then
    say "正在安装：${missing[*]}"
    install_packages "${missing[@]}"
  fi
}

ensure_docker() {
  if docker_bin info >/dev/null 2>&1 && docker_bin compose version >/dev/null 2>&1; then
    say "Docker 与 Compose 已就绪。"
    return
  fi
  say "未检测到可用的 Docker，将安装 Docker Engine 与 Compose 插件。"
  confirm "继续安装 Docker？" "y" || die "需要 Docker 才能部署。"
  curl -fsSL https://get.docker.com | run_root sh
  if need_bin systemctl; then
    run_root systemctl enable --now docker
  fi
  docker_bin info >/dev/null 2>&1 || die "Docker 安装完成但仍无法使用，请检查服务状态。"
  docker_bin compose version >/dev/null 2>&1 || die "未找到 docker compose 插件。"
  if [[ "$(id -u)" -ne 0 ]]; then
    run_root usermod -aG docker "$USER" || true
    say "已将当前用户加入 docker 组，若后续命令失败，请重新登录后再执行一次本脚本。"
  fi
}

clone_app() {
  local dir="$1"
  if [[ -d "$dir/.git" ]]; then
    say "安装目录已存在，正在更新代码。"
    git -C "$dir" pull --ff-only
    return
  fi
  if [[ -d "$dir" && -n "$(ls -A "$dir")" ]]; then
    die "目录 $dir 已存在且不是 Git 仓库，请换一个安装路径。"
  fi
  say "正在克隆 $APP_REPO"
  git clone "$APP_REPO" "$dir"
}

has_secrets_access() {
  need_bin gh && gh auth status >/dev/null 2>&1 && gh repo view "$SECRETS_REPO" >/dev/null 2>&1
}

secrets_has_env() {
  has_secrets_access || return 1
  gh api "repos/${SECRETS_REPO}/contents/.env" >/dev/null 2>&1
}

pull_secrets() {
  local dir="$1"
  local tmp="$dir/.server-secrets"
  rm -rf "$tmp"
  say "正在从安全仓库拉取配置。"
  gh repo clone "$SECRETS_REPO" "$tmp"
  [[ -f "$tmp/.env" ]] || die "安全仓库中没有 .env。"
  cp "$tmp/.env" "$dir/.env"
  mkdir -p "$dir/secrets"
  if [[ -f "$tmp/jwt-private.pem" && -f "$tmp/jwt-public.pem" ]]; then
    cp "$tmp/jwt-private.pem" "$dir/secrets/jwt-private.pem"
    cp "$tmp/jwt-public.pem" "$dir/secrets/jwt-public.pem"
    chmod 600 "$dir/secrets/jwt-private.pem"
  else
    generate_jwt "$dir"
  fi
}

write_env() {
  local dir="$1"
  local host_port="$2"
  local postgres_password="$3"
  local redis_password="$4"
  local minio_access="$5"
  local minio_secret="$6"
  cat >"$dir/.env" <<EOF
HOST_PORT=${host_port}
HTTP_PORT=8080
POSTGRES_USER=store
POSTGRES_PASSWORD=${postgres_password}
POSTGRES_DB=store
DATABASE_URL=postgres://store:${postgres_password}@postgres:5432/store?sslmode=disable
REDIS_PASSWORD=${redis_password}
REDIS_URL=redis://:${redis_password}@redis:6379/0
MINIO_ENDPOINT=minio:9000
MINIO_ACCESS_KEY=${minio_access}
MINIO_SECRET_KEY=${minio_secret}
MINIO_BUCKET=store
MINIO_USE_SSL=false
JWT_PRIVATE_KEY=
JWT_PUBLIC_KEY=
JWT_PRIVATE_KEY_FILE=/run/secrets/jwt-private.pem
JWT_PUBLIC_KEY_FILE=/run/secrets/jwt-public.pem
EOF
  chmod 600 "$dir/.env"
}

generate_jwt() {
  local dir="$1"
  mkdir -p "$dir/secrets"
  openssl genrsa -out "$dir/secrets/jwt-private.pem" 2048 >/dev/null 2>&1
  openssl rsa -in "$dir/secrets/jwt-private.pem" -pubout -out "$dir/secrets/jwt-public.pem" >/dev/null 2>&1
  chmod 600 "$dir/secrets/jwt-private.pem"
}

create_config() {
  local dir="$1"
  local host_port postgres_password redis_password minio_access minio_secret
  host_port="$(prompt "对外访问端口" "8080")"
  postgres_password="$(prompt "PostgreSQL 密码，回车则自动生成" "")"
  redis_password="$(prompt "Redis 密码，回车则自动生成" "")"
  minio_access="$(prompt "MinIO Access Key，回车则自动生成" "")"
  minio_secret="$(prompt "MinIO Secret Key，回车则自动生成" "")"
  [[ -n "$postgres_password" ]] || postgres_password="$(openssl rand -hex 16)"
  [[ -n "$redis_password" ]] || redis_password="$(openssl rand -hex 16)"
  [[ -n "$minio_access" ]] || minio_access="$(openssl rand -hex 8)"
  [[ -n "$minio_secret" ]] || minio_secret="$(openssl rand -hex 16)"
  generate_jwt "$dir"
  write_env "$dir" "$host_port" "$postgres_password" "$redis_password" "$minio_access" "$minio_secret"
  say "已写入 $dir/.env，并生成 JWT 密钥。"
}

push_secrets() {
  local dir="$1"
  local tmp="$dir/.server-secrets"
  if [[ ! -d "$tmp/.git" ]]; then
    gh repo clone "$SECRETS_REPO" "$tmp"
  fi
  cp "$dir/.env" "$tmp/.env"
  cp "$dir/secrets/jwt-private.pem" "$tmp/jwt-private.pem"
  cp "$dir/secrets/jwt-public.pem" "$tmp/jwt-public.pem"
  git -C "$tmp" add .env jwt-private.pem jwt-public.pem
  if git -C "$tmp" diff --cached --quiet; then
    say "安全仓库已是最新配置。"
    return
  fi
  git -C "$tmp" commit -m "chore: sync production secrets"
  git -C "$tmp" push origin HEAD
  say "配置已推送到 $SECRETS_REPO。"
}

wait_ready() {
  local port="$1"
  local url="http://127.0.0.1:${port}/v1/auth/.well-known/jwks.json"
  local i
  say "等待服务就绪。"
  for i in $(seq 1 90); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  return 1
}

public_ip() {
  curl -fsS --max-time 5 https://ifconfig.me || hostname -I 2>/dev/null | awk '{print $1}'
}

main() {
  [[ "$(uname -s)" == "Linux" ]] || die "请在 Linux 服务器上运行本脚本。"

  say ""
  say "========================================"
  say " Store Server Docker 安装向导"
  say "========================================"
  say ""

  say "[1/6] 检查依赖"
  ensure_tools
  ensure_docker

  say ""
  say "[2/6] 选择安装目录"
  local default_dir="/opt/store-server"
  if [[ "$(id -u)" -ne 0 ]]; then
    default_dir="$HOME/store-server"
  fi
  local install_dir
  install_dir="$(prompt "安装目录" "$default_dir")"

  say ""
  say "[3/6] 拉取代码"
  if [[ ! -d "$install_dir" && ! -w "$(dirname "$install_dir")" ]]; then
    run_root mkdir -p "$install_dir"
    run_root chown "$(id -u):$(id -g)" "$install_dir"
  fi
  clone_app "$install_dir"
  cd "$install_dir"
  mkdir -p secrets

  say ""
  say "[4/6] 配置变量"
  local used_secrets=0
  if [[ -f "$install_dir/.env" ]]; then
    say "发现已有 .env。"
    if confirm "沿用现有配置？" "y"; then
      used_secrets=1
    fi
  fi
  if [[ "$used_secrets" -eq 0 ]] && secrets_has_env; then
    if confirm "从 GitHub 安全仓库拉取配置？" "y"; then
      pull_secrets "$install_dir"
      used_secrets=1
    fi
  fi
  if [[ "$used_secrets" -eq 0 ]]; then
    create_config "$install_dir"
  fi
  grep -q '^HOST_PORT=' "$install_dir/.env" || die ".env 缺少 HOST_PORT。"

  say ""
  say "[5/6] 启动 Docker 服务"
  compose -f "$install_dir/docker-compose.yml" --env-file "$install_dir/.env" up -d --build

  local host_port
  host_port="$(grep '^HOST_PORT=' "$install_dir/.env" | cut -d= -f2-)"

  say ""
  say "[6/6] 验证服务"
  if wait_ready "$host_port"; then
    say "服务已启动。"
  else
    say "服务尚未通过健康检查，请查看日志："
    say "  docker compose -f $install_dir/docker-compose.yml logs --tail=80 app"
    exit 1
  fi

  if has_secrets_access && confirm "把当前配置备份到 GitHub 安全仓库？" "y"; then
    push_secrets "$install_dir"
  fi

  local ip
  ip="$(public_ip)"
  say ""
  say "========================================"
  say " 部署完成"
  say "========================================"
  say "安装目录：$install_dir"
  say "访问地址：http://${ip:-127.0.0.1}:${host_port}"
  say "本机探测：http://127.0.0.1:${host_port}/v1/auth/.well-known/jwks.json"
  say "MinIO 控制台：http://127.0.0.1:9001"
  say "请在防火墙或宝塔面板放行 ${host_port} 端口。"
}

main "$@"
