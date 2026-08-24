#!/usr/bin/env bash
# 一键编译脚本：编译二进制并连同配置文件一起输出到 output/ 目录。
#
# 用法：
#   ./build.sh                 # 编译当前平台
#   ./build.sh linux amd64     # 交叉编译指定平台/架构
set -euo pipefail

cd "$(dirname "$0")"

BINARY="gitlab-ci-mr-pipelines-hooks"
OUTPUT_DIR="output"
CONFIG_SRC="config.example.yaml"

# 可选交叉编译参数
GOOS="${1:-$(go env GOOS)}"
GOARCH="${2:-$(go env GOARCH)}"

echo "==> 编译 ${GOOS}/${GOARCH} ..."
mkdir -p "${OUTPUT_DIR}"

# 交叉编译时给二进制加平台后缀，避免覆盖
OUT_BIN="${BINARY}"
if [[ "${GOOS}" != "$(go env GOOS)" || "${GOARCH}" != "$(go env GOARCH)" ]]; then
  OUT_BIN="${BINARY}-${GOOS}-${GOARCH}"
fi

CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
  go build -trimpath -ldflags="-s -w" -o "${OUTPUT_DIR}/${OUT_BIN}" .

echo "==> 复制配置文件 ..."
cp "${CONFIG_SRC}" "${OUTPUT_DIR}/config.yaml"

echo "==> 构建完成，输出目录："
ls -lh "${OUTPUT_DIR}"
