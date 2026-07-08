#!/usr/bin/env bash
# Fetch envtest kube-apiserver + etcd for local integration tests.
# Installs into .tools/envtest/ (gitignored). Idempotent.
set -euo pipefail
cd "$(dirname "$0")/.."

ENVTEST_VERSION="${ENVTEST_VERSION:-1.30.0}"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
esac

DEST=".tools/envtest/${ENVTEST_VERSION}-${OS}-${ARCH}"
MARKER="${DEST}/.installed"
KUBEBUILDER_DIR="${HOME}/Library/Application Support/io.kubebuilder.envtest/k8s/${ENVTEST_VERSION}-${OS}-${ARCH}"
if [ ! -x "${KUBEBUILDER_DIR}/kube-apiserver" ]; then
  KUBEBUILDER_DIR="${HOME}/.local/share/kubebuilder-envtest/k8s/${ENVTEST_VERSION}-${OS}-${ARCH}"
fi

if [ -f "$MARKER" ]; then
  echo "envtest already present: $DEST" >&2
  echo "$DEST"
  exit 0
fi

if [ -x "${KUBEBUILDER_DIR}/kube-apiserver" ]; then
  echo "Using kubebuilder envtest: $KUBEBUILDER_DIR" >&2
  mkdir -p "$DEST"
  ln -sf "${KUBEBUILDER_DIR}/kube-apiserver" "${DEST}/kube-apiserver"
  ln -sf "${KUBEBUILDER_DIR}/etcd" "${DEST}/etcd"
  ln -sf "${KUBEBUILDER_DIR}/kubectl" "${DEST}/kubectl" 2>/dev/null || true
  date -u +"%Y-%m-%dT%H:%M:%SZ" > "$MARKER"
  echo "$DEST"
  exit 0
fi

URL="https://github.com/kubernetes-sigs/controller-tools/releases/download/envtest-v${ENVTEST_VERSION}/envtest-v${ENVTEST_VERSION}-${OS}-${ARCH}.tar.gz"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "Downloading envtest v${ENVTEST_VERSION} (${OS}/${ARCH}) ..." >&2
mkdir -p "$DEST"
curl -fsSL "$URL" -o "$TMP/envtest.tar.gz"
tar -xzf "$TMP/envtest.tar.gz" -C "$TMP"
# Archives use controller-tools/envtest/{kube-apiserver,etcd}
if [ -d "$TMP/controller-tools/envtest" ]; then
  cp -f "$TMP/controller-tools/envtest/"* "$DEST/"
elif [ -d "$TMP/kubebuilder/bin" ]; then
  cp -f "$TMP/kubebuilder/bin/"* "$DEST/"
else
  echo "unexpected envtest archive layout:" >&2
  find "$TMP" -maxdepth 3 -type f >&2
  exit 1
fi
chmod +x "$DEST/"* 2>/dev/null || true
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$MARKER"
echo "Installed envtest to $DEST" >&2
echo "$DEST"
