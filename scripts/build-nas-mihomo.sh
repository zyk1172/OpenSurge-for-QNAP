#!/bin/sh
set -eu

# The Docker builder and Linux network lab must exercise the same security
# dependency graph. Callers fetch and verify the immutable source commit first.
if [ "$#" -ne 3 ]; then
  echo "usage: build-nas-mihomo.sh SOURCE_DIR OUTPUT VERSION" >&2
  exit 2
fi
cd "$1"
output=$2
version=$3
test -f go.mod
target_os=${GOOS:-linux}
target_arch=${GOARCH:-$(go env GOARCH)}
case "$target_arch" in
  amd64) export GOAMD64=v1 ;;
  arm64) : ;;
  *) echo "unsupported target architecture: $target_arch" >&2; exit 1 ;;
esac
go mod edit -go=1.26.11
go get golang.org/x/crypto@v0.59.0 golang.org/x/net@v0.62.0 \
  github.com/klauspost/compress@v1.20.2 github.com/andybalholm/brotli@v1.2.7 \
  github.com/insomniacslk/dhcp@v0.0.0-20260719225207-c76316d4aa82
go mod tidy
CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
  go build -tags with_gvisor -trimpath \
    -ldflags="-X github.com/metacubex/mihomo/constant.Version=v${version}+opensurge-security -X github.com/metacubex/mihomo/constant.BuildTime=OpenSurge-security-rebuild -w -s -buildid=" \
    -o "$output" .
