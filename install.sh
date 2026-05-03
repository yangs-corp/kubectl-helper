#!/usr/bin/env bash
set -e

INSTALL_DIR="/usr/local/bin"

install_bin() {
  local name=$1
  local build_path=$2
  echo "Building $name..."
  go build -o "$name" "$build_path"
  echo "Installing to $INSTALL_DIR/$name..."
  if [ -w "$INSTALL_DIR" ]; then
    cp "$name" "$INSTALL_DIR/$name"
  else
    sudo cp "$name" "$INSTALL_DIR/$name"
  fi
}

install_bin kubectl-log ./cmd/log/
install_bin kubectl-node ./cmd/node/

echo ""
echo "Done."
echo "  kubectl log   — deployment 로그 뷰어"
echo "  kubectl node  — 노드 관리 (drain / cordon / uncordon)"
