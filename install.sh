#!/usr/bin/env bash
set -euo pipefail

REPO="${S000_REPO:-trip1/s000}"
VERSION="${S000_VERSION:-latest}"
INIT_SYSTEM="${S000_INIT_SYSTEM:-auto}"
INSTALL_DIR="${S000_INSTALL_DIR:-/usr/local/bin}"
SERVICE_NAME="${S000_SERVICE_NAME:-s000}"
SERVICE_USER="${S000_SERVICE_USER:-s000}"
SERVICE_GROUP="${S000_SERVICE_GROUP:-s000}"
WORK_DIR="${S000_WORK_DIR:-/var/lib/s000}"
DATA_DIR="${S000_DATA_DIR:-${WORK_DIR}/data}"
METADATA_BACKEND="${S000_METADATA_BACKEND:-sqlite}"
METADATA_DSN="${S000_METADATA_DSN:-file:${DATA_DIR}/s000-metadata.db}"
SERVICE_ADDR="${S000_ADDR:-:9000}"
ADMIN_ACCESS_KEY="${S000_ADMIN_ACCESS_KEY:-admin}"
ADMIN_SECRET_KEY="${S000_ADMIN_SECRET_KEY:-change-me}"
SKIP_DEPS=0
START_SERVICE=1

usage() {
  cat <<'EOF'
Usage: install.sh [options]

Installs s000 from GitHub Releases, installs required dependencies,
and configures a service for systemd or OpenRC.

Options:
  --version <tag>         Release tag (default: latest)
  --repo <owner/repo>     GitHub repo (default: trip1/s000)
  --init <mode>           auto|systemd|openrc|none (default: auto)
  --install-dir <path>    Binary install directory (default: /usr/local/bin)
  --work-dir <path>       Service working directory (default: /var/lib/s000)
  --data-dir <path>       Data directory (default: /var/lib/s000/data)
  --access-key <value>    Bootstrap admin access key (default: admin)
  --secret-key <value>    Bootstrap admin secret key (default: change-me)
  --metadata-backend <v>  sqlite|libsql|postgresql|mariadb|valkey
  --metadata-dsn <value>  Metadata DSN override
  --addr <value>          Bind address (default: :9000)
  --skip-deps             Skip dependency installation
  --skip-start            Do not enable/start the service
  -h, --help              Show this help

Examples:
  sudo ./install.sh --version v0.1.0
  sudo ./install.sh --init systemd --access-key admin --secret-key 'strong-secret'
  sudo ./install.sh --init openrc --metadata-backend sqlite
EOF
}

log() {
  printf '[install] %s\n' "$*"
}

die() {
  printf '[install] ERROR: %s\n' "$*" >&2
  exit 1
}

require_root() {
  if [[ "${EUID}" -ne 0 ]]; then
    die "run as root (use sudo)"
  fi
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

detect_os() {
  local raw
  raw="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "${raw}" in
    linux)
      GOOS="linux"
      ;;
    freebsd)
      GOOS="freebsd"
      ;;
    darwin)
      GOOS="darwin"
      ;;
    *)
      die "unsupported OS: ${raw}"
      ;;
  esac
}

detect_arch() {
  local raw
  raw="$(uname -m)"
  case "${raw}" in
    x86_64|amd64)
      GOARCH="amd64"
      ;;
    aarch64|arm64)
      GOARCH="arm64"
      ;;
    *)
      die "unsupported architecture: ${raw}"
      ;;
  esac
}

install_dependencies_linux() {
  if [[ "${SKIP_DEPS}" -eq 1 ]]; then
    return
  fi

  local pkgs
  pkgs=(curl ca-certificates tar)

  if command_exists apt-get; then
    apt-get update -y
    apt-get install -y "${pkgs[@]}" coreutils
    return
  fi
  if command_exists dnf; then
    dnf install -y "${pkgs[@]}" coreutils
    return
  fi
  if command_exists yum; then
    yum install -y "${pkgs[@]}" coreutils
    return
  fi
  if command_exists apk; then
    apk add --no-cache "${pkgs[@]}" coreutils
    return
  fi
  if command_exists pacman; then
    pacman -Sy --noconfirm "${pkgs[@]}" coreutils
    return
  fi
  if command_exists zypper; then
    zypper --non-interactive install "${pkgs[@]}" coreutils
    return
  fi

  die "no supported Linux package manager found"
}

install_dependencies_freebsd() {
  if [[ "${SKIP_DEPS}" -eq 1 ]]; then
    return
  fi

  if ! command_exists pkg; then
    die "pkg is required to install dependencies on FreeBSD"
  fi

  pkg update -f >/dev/null
  pkg install -y curl ca_root_nss
}

install_dependencies() {
  if [[ "${GOOS}" == "linux" ]]; then
    install_dependencies_linux
  elif [[ "${GOOS}" == "freebsd" ]]; then
    install_dependencies_freebsd
  fi

  command_exists tar || die "tar is required"
  command_exists curl || die "curl is required"
}

resolve_version() {
  if [[ "${VERSION}" != "latest" ]]; then
    RELEASE_TAG="${VERSION}"
    return
  fi

  local api_url
  api_url="https://api.github.com/repos/${REPO}/releases/latest"
  RELEASE_TAG="$(curl -fsSL "${api_url}" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"

  [[ -n "${RELEASE_TAG}" ]] || die "failed to resolve latest release tag from ${api_url}"
}

calc_sha256() {
  local file
  file="$1"

  if command_exists sha256sum; then
    sha256sum "${file}" | awk '{print $1}'
    return
  fi
  if command_exists shasum; then
    shasum -a 256 "${file}" | awk '{print $1}'
    return
  fi
  if command_exists openssl; then
    openssl dgst -sha256 "${file}" | awk '{print $2}'
    return
  fi

  die "no SHA-256 tool available (sha256sum/shasum/openssl)"
}

download_release() {
  TMP_DIR="$(mktemp -d)"
  trap 'rm -rf "${TMP_DIR}"' EXIT

  ARCHIVE_NAME="s000-${RELEASE_TAG}-${GOOS}-${GOARCH}.tar.gz"
  BASE_URL="https://github.com/${REPO}/releases/download/${RELEASE_TAG}"
  ARCHIVE_PATH="${TMP_DIR}/${ARCHIVE_NAME}"
  CHECKSUMS_PATH="${TMP_DIR}/checksums.txt"

  log "downloading ${ARCHIVE_NAME}"
  curl -fL --retry 3 --retry-delay 1 -o "${ARCHIVE_PATH}" "${BASE_URL}/${ARCHIVE_NAME}"
  curl -fL --retry 3 --retry-delay 1 -o "${CHECKSUMS_PATH}" "${BASE_URL}/checksums.txt"

  local expected actual
  expected="$(awk -v f="${ARCHIVE_NAME}" '$2 == f { print $1 }' "${CHECKSUMS_PATH}")"
  [[ -n "${expected}" ]] || die "missing checksum for ${ARCHIVE_NAME}"

  actual="$(calc_sha256 "${ARCHIVE_PATH}")"
  [[ "${actual}" == "${expected}" ]] || die "checksum mismatch for ${ARCHIVE_NAME}"

  tar -xzf "${ARCHIVE_PATH}" -C "${TMP_DIR}"
  STAGE_DIR="${TMP_DIR}/s000-${RELEASE_TAG}-${GOOS}-${GOARCH}"
  [[ -d "${STAGE_DIR}" ]] || die "release archive layout unexpected"
}

install_binary() {
  install -d -m 0755 "${INSTALL_DIR}"
  install -m 0755 "${STAGE_DIR}/s000" "${INSTALL_DIR}/s000"
  log "installed binary to ${INSTALL_DIR}/s000"
}

ensure_user_group_linux() {
  if ! getent group "${SERVICE_GROUP}" >/dev/null 2>&1; then
    groupadd --system "${SERVICE_GROUP}"
  fi

  if ! id -u "${SERVICE_USER}" >/dev/null 2>&1; then
    useradd --system --home "${WORK_DIR}" --shell /usr/sbin/nologin --gid "${SERVICE_GROUP}" "${SERVICE_USER}"
  fi
}

ensure_user_group_openrc_fallback() {
  if ! getent group "${SERVICE_GROUP}" >/dev/null 2>&1; then
    addgroup -S "${SERVICE_GROUP}" || true
  fi

  if ! id -u "${SERVICE_USER}" >/dev/null 2>&1; then
    adduser -S -D -H -h "${WORK_DIR}" -s /sbin/nologin -G "${SERVICE_GROUP}" "${SERVICE_USER}" || true
  fi
}

ensure_user_group_freebsd() {
  if ! pw groupshow "${SERVICE_GROUP}" >/dev/null 2>&1; then
    pw groupadd "${SERVICE_GROUP}"
  fi

  if ! id -u "${SERVICE_USER}" >/dev/null 2>&1; then
    pw useradd "${SERVICE_USER}" -g "${SERVICE_GROUP}" -d "${WORK_DIR}" -s /usr/sbin/nologin -m
  fi
}

prepare_dirs() {
  install -d -m 0755 "${WORK_DIR}" "${DATA_DIR}"
  chown -R "${SERVICE_USER}:${SERVICE_GROUP}" "${WORK_DIR}"
}

detect_init_system() {
  if [[ "${INIT_SYSTEM}" != "auto" ]]; then
    SELECTED_INIT="${INIT_SYSTEM}"
    return
  fi

  if [[ "${GOOS}" == "linux" ]]; then
    if command_exists systemctl && [[ -d /run/systemd/system ]]; then
      SELECTED_INIT="systemd"
      return
    fi
    if command_exists rc-service && [[ -d /etc/init.d ]]; then
      SELECTED_INIT="openrc"
      return
    fi
  fi

  SELECTED_INIT="none"
}

write_systemd_unit() {
  local env_file unit_file
  env_file="/etc/default/${SERVICE_NAME}"
  unit_file="/etc/systemd/system/${SERVICE_NAME}.service"

  cat >"${env_file}" <<EOF
S000_ADDR=${SERVICE_ADDR}
S000_DATA_DIR=${DATA_DIR}
S000_METADATA_BACKEND=${METADATA_BACKEND}
S000_METADATA_DSN=${METADATA_DSN}
S000_ADMIN_ACCESS_KEY=${ADMIN_ACCESS_KEY}
S000_ADMIN_SECRET_KEY=${ADMIN_SECRET_KEY}
EOF

  cat >"${unit_file}" <<EOF
[Unit]
Description=s000 S3-compatible object storage
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_GROUP}
WorkingDirectory=${WORK_DIR}
EnvironmentFile=-${env_file}
ExecStart=${INSTALL_DIR}/s000
Restart=on-failure
RestartSec=3
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  if [[ "${START_SERVICE}" -eq 1 ]]; then
    systemctl enable --now "${SERVICE_NAME}"
  fi

  log "configured systemd unit ${unit_file}"
}

write_openrc_unit() {
  local init_file conf_file
  init_file="/etc/init.d/${SERVICE_NAME}"
  conf_file="/etc/conf.d/${SERVICE_NAME}"

  cat >"${init_file}" <<'EOF'
#!/sbin/openrc-run

name="s000"
description="s000 S3-compatible object storage"
command="/usr/local/bin/s000"
pidfile="/run/${RC_SVCNAME}.pid"
command_background="yes"
command_user="${S000_SERVICE_USER:-s000}:${S000_SERVICE_GROUP:-s000}"
directory="${S000_WORK_DIR:-/var/lib/s000}"
command_env="S000_ADDR=${S000_ADDR:-:9000} S000_DATA_DIR=${S000_DATA_DIR:-/var/lib/s000/data} S000_METADATA_BACKEND=${S000_METADATA_BACKEND:-sqlite} S000_METADATA_DSN=${S000_METADATA_DSN:-file:/var/lib/s000/data/s000-metadata.db} S000_ADMIN_ACCESS_KEY=${S000_ADMIN_ACCESS_KEY:-admin} S000_ADMIN_SECRET_KEY=${S000_ADMIN_SECRET_KEY:-change-me}"

depend() {
  need net
}
EOF

  sed -i "s|/usr/local/bin/s000|${INSTALL_DIR}/s000|g" "${init_file}"
  chmod 0755 "${init_file}"

  cat >"${conf_file}" <<EOF
S000_SERVICE_USER=${SERVICE_USER}
S000_SERVICE_GROUP=${SERVICE_GROUP}
S000_WORK_DIR=${WORK_DIR}
S000_ADDR=${SERVICE_ADDR}
S000_DATA_DIR=${DATA_DIR}
S000_METADATA_BACKEND=${METADATA_BACKEND}
S000_METADATA_DSN=${METADATA_DSN}
S000_ADMIN_ACCESS_KEY=${ADMIN_ACCESS_KEY}
S000_ADMIN_SECRET_KEY=${ADMIN_SECRET_KEY}
EOF

  if [[ "${START_SERVICE}" -eq 1 ]]; then
    rc-update add "${SERVICE_NAME}" default
    rc-service "${SERVICE_NAME}" restart || rc-service "${SERVICE_NAME}" start
  fi

  log "configured OpenRC service ${init_file}"
}

configure_service() {
  case "${SELECTED_INIT}" in
    systemd)
      write_systemd_unit
      ;;
    openrc)
      write_openrc_unit
      ;;
    none)
      log "no init system configured; run manually: ${INSTALL_DIR}/s000"
      ;;
    *)
      die "unsupported init system: ${SELECTED_INIT}"
      ;;
  esac
}

parse_args() {
  while [[ "$#" -gt 0 ]]; do
    case "$1" in
      --version)
        VERSION="$2"
        shift 2
        ;;
      --repo)
        REPO="$2"
        shift 2
        ;;
      --init)
        INIT_SYSTEM="$2"
        shift 2
        ;;
      --install-dir)
        INSTALL_DIR="$2"
        shift 2
        ;;
      --work-dir)
        WORK_DIR="$2"
        DATA_DIR="${WORK_DIR}/data"
        METADATA_DSN="file:${DATA_DIR}/s000-metadata.db"
        shift 2
        ;;
      --data-dir)
        DATA_DIR="$2"
        METADATA_DSN="file:${DATA_DIR}/s000-metadata.db"
        shift 2
        ;;
      --metadata-backend)
        METADATA_BACKEND="$2"
        shift 2
        ;;
      --metadata-dsn)
        METADATA_DSN="$2"
        shift 2
        ;;
      --addr)
        SERVICE_ADDR="$2"
        shift 2
        ;;
      --access-key)
        ADMIN_ACCESS_KEY="$2"
        shift 2
        ;;
      --secret-key)
        ADMIN_SECRET_KEY="$2"
        shift 2
        ;;
      --skip-deps)
        SKIP_DEPS=1
        shift
        ;;
      --skip-start)
        START_SERVICE=0
        shift
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        die "unknown option: $1"
        ;;
    esac
  done
}

validate_inputs() {
  case "${INIT_SYSTEM}" in
    auto|systemd|openrc|none)
      ;;
    *)
      die "--init must be one of: auto, systemd, openrc, none"
      ;;
  esac

  case "${METADATA_BACKEND}" in
    sqlite|libsql|postgresql|mariadb|valkey)
      ;;
    *)
      die "unsupported metadata backend: ${METADATA_BACKEND}"
      ;;
  esac
}

main() {
  parse_args "$@"
  validate_inputs
  require_root
  detect_os
  detect_arch
  install_dependencies
  resolve_version
  download_release
  install_binary

  if [[ "${GOOS}" == "linux" ]]; then
    if command_exists useradd; then
      ensure_user_group_linux
    else
      ensure_user_group_openrc_fallback
    fi
  elif [[ "${GOOS}" == "freebsd" ]]; then
    ensure_user_group_freebsd
  fi

  prepare_dirs
  detect_init_system
  configure_service

  log "install complete"
  log "binary: ${INSTALL_DIR}/s000"
  log "release: ${RELEASE_TAG} (${GOOS}/${GOARCH})"
}

main "$@"
