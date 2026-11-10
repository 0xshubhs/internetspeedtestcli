#!/bin/sh
set -eu

REPO="0xshubhs/internetspeedtestcli"
MODULE="github.com/$REPO"
BIN="netspeed"
BASE_URL="${NETSPEED_BASE_URL:-https://github.com/$REPO/releases}"
VERSION="${NETSPEED_VERSION:-latest}"
INSTALL_DIR="${NETSPEED_INSTALL_DIR:-}"
FROM_SOURCE=0
UNINSTALL=0
TMP=""
BUILT=""
SCRIPT_DIR=""

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
	BOLD=$(printf '\033[1m')
	GREEN=$(printf '\033[32m')
	YELLOW=$(printf '\033[33m')
	RED=$(printf '\033[31m')
	RESET=$(printf '\033[0m')
else
	BOLD="" GREEN="" YELLOW="" RED="" RESET=""
fi

say() { printf '%s\n' "$*"; }
step() { printf '%s==>%s %s\n' "$BOLD" "$RESET" "$*"; }
warn() { printf '%swarning:%s %s\n' "$YELLOW" "$RESET" "$*" >&2; }
die() {
	printf '%serror:%s %s\n' "$RED" "$RESET" "$*" >&2
	exit 1
}
has() { command -v "$1" >/dev/null 2>&1; }

usage() {
	cat <<EOF
Install netspeed, the terminal internet speed test.

Usage:
  ./install.sh [options]
  curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh | sh
  curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh | sh -s -- [options]

Downloads the prebuilt binary for this OS and CPU from GitHub Releases,
verifies its SHA-256 checksum and installs it. If no prebuilt binary is
available, it builds from source with Go instead.

Options:
  -d, --dir DIR       install directory (default: ~/.local/bin, or /usr/local/bin as root)
  -v, --version TAG   release to install, for example v0.1.0 (default: latest)
  -s, --source        build from source with Go instead of downloading
  -u, --uninstall     remove netspeed from the install directory
  -h, --help          show this help

Environment:
  NETSPEED_INSTALL_DIR, NETSPEED_VERSION   same as --dir and --version
  NETSPEED_BASE_URL                        release download base URL, for mirrors
EOF
}

need_arg() {
	if [ $# -lt 2 ] || [ -z "$2" ]; then
		die "$1 needs a value"
	fi
}

parse_args() {
	while [ $# -gt 0 ]; do
		case "$1" in
		-d | --dir)
			need_arg "$@"
			INSTALL_DIR=$2
			shift 2
			;;
		--dir=*)
			INSTALL_DIR=${1#*=}
			shift
			;;
		-v | --version)
			need_arg "$@"
			VERSION=$2
			shift 2
			;;
		--version=*)
			VERSION=${1#*=}
			shift
			;;
		-s | --source)
			FROM_SOURCE=1
			shift
			;;
		-u | --uninstall)
			UNINSTALL=1
			shift
			;;
		-h | --help)
			usage
			exit 0
			;;
		*)
			die "unknown option: $1 (see --help)"
			;;
		esac
	done

	case "$VERSION" in
	latest | v*) ;;
	*) VERSION="v$VERSION" ;;
	esac

	if [ -z "$INSTALL_DIR" ]; then
		if [ "$(id -u)" = 0 ]; then
			INSTALL_DIR=/usr/local/bin
		else
			INSTALL_DIR="$HOME/.local/bin"
		fi
	fi
}

detect_platform() {
	PREBUILT=1
	os=$(uname -s)
	case "$os" in
	Linux) OS=linux ;;
	Darwin) OS=darwin ;;
	MINGW* | MSYS* | CYGWIN*) OS=windows ;;
	*)
		OS=$os
		PREBUILT=0
		;;
	esac

	machine=$(uname -m)
	case "$machine" in
	x86_64 | amd64) ARCH=amd64 ;;
	aarch64 | arm64 | armv8*) ARCH=arm64 ;;
	*)
		ARCH=$machine
		PREBUILT=0
		;;
	esac

	if [ "$OS" = darwin ] && [ "$ARCH" = amd64 ]; then
		if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
			ARCH=arm64
		fi
	fi

	EXT=""
	if [ "$OS" = windows ]; then
		EXT=".exe"
	fi
	ASSET="$BIN-$OS-$ARCH$EXT"
	TARGET="$INSTALL_DIR/$BIN$EXT"
}

fetch() {
	if has curl; then
		curl -fsSL --retry 2 --connect-timeout 15 -o "$2" "$1"
	elif has wget; then
		wget -q -T 30 -O "$2" "$1"
	else
		return 2
	fi
}

sha256() {
	if has sha256sum; then
		sha256sum "$1" | cut -d' ' -f1
	elif has shasum; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif has openssl; then
		openssl dgst -sha256 "$1" | sed 's/^.*= //'
	else
		return 1
	fi
}

verify() {
	expected=$(awk -v f="$ASSET" '$2 == f || $2 == "*" f { print $1; exit }' "$2")
	if [ -z "$expected" ]; then
		warn "$ASSET is not listed in SHA256SUMS, skipping the checksum check"
		return 0
	fi
	if ! actual=$(sha256 "$1"); then
		warn "no SHA-256 tool found, skipping the checksum check"
		return 0
	fi
	if [ "$expected" != "$actual" ]; then
		die "checksum mismatch for $ASSET (expected $expected, got $actual)"
	fi
	say "    checksum verified"
}

download_release() {
	if [ "$PREBUILT" != 1 ]; then
		warn "no prebuilt binary for $OS/$ARCH"
		return 1
	fi
	if [ "$VERSION" = latest ]; then
		url="$BASE_URL/latest/download"
	else
		url="$BASE_URL/download/$VERSION"
	fi

	step "Downloading $ASSET ($VERSION)"
	if fetch "$url/$ASSET" "$TMP/$ASSET" 2>/dev/null; then
		if fetch "$url/SHA256SUMS" "$TMP/SHA256SUMS" 2>/dev/null; then
			verify "$TMP/$ASSET" "$TMP/SHA256SUMS"
		else
			warn "this release has no SHA256SUMS, skipping the checksum check"
		fi
		BUILT="$TMP/$ASSET"
		return 0
	fi

	if has gh && gh auth status >/dev/null 2>&1; then
		say "    direct download failed, trying the GitHub CLI"
		tag=""
		if [ "$VERSION" != latest ]; then
			tag=$VERSION
		fi
		if gh release download $tag -R "$REPO" -p "$ASSET" -D "$TMP" --clobber >/dev/null 2>&1; then
			if gh release download $tag -R "$REPO" -p SHA256SUMS -D "$TMP" --clobber >/dev/null 2>&1; then
				verify "$TMP/$ASSET" "$TMP/SHA256SUMS"
			else
				warn "this release has no SHA256SUMS, skipping the checksum check"
			fi
			BUILT="$TMP/$ASSET"
			return 0
		fi
	fi
	return 1
}

build_source() {
	if ! has go; then
		die "no prebuilt binary could be downloaded and Go is not installed. Install Go 1.26 or newer from https://go.dev/dl/ and run this again."
	fi

	src=""
	if [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/go.mod" ] && grep -q "^module $MODULE\$" "$SCRIPT_DIR/go.mod"; then
		src=$SCRIPT_DIR
	fi

	if [ -n "$src" ] && [ "$VERSION" = latest ]; then
		ver=$(git -C "$src" describe --tags --always --dirty 2>/dev/null || echo dev)
		step "Building $BIN $ver from $src"
		BUILT="$TMP/$BIN$EXT"
		(cd "$src" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$ver" -o "$BUILT" ./cmd/netspeed) ||
			die "build failed"
	else
		step "Building $BIN from $MODULE@$VERSION"
		GOBIN="$TMP/gobin" CGO_ENABLED=0 go install -trimpath "$MODULE/cmd/netspeed@$VERSION" ||
			die "go install failed"
		BUILT="$TMP/gobin/$BIN$EXT"
	fi
}

place() {
	chmod 755 "$1"
	if mkdir -p "$INSTALL_DIR" 2>/dev/null && [ -w "$INSTALL_DIR" ]; then
		INSTALL_DIR=$(cd "$INSTALL_DIR" && pwd)
		TARGET="$INSTALL_DIR/$BIN$EXT"
		step "Installing to $TARGET"
		cp "$1" "$TARGET.new.$$"
		chmod 755 "$TARGET.new.$$"
		mv -f "$TARGET.new.$$" "$TARGET"
		return 0
	fi
	if [ "$(id -u)" != 0 ] && has sudo; then
		step "Installing to $TARGET (needs sudo)"
		sudo mkdir -p "$INSTALL_DIR"
		sudo cp "$1" "$TARGET.new.$$"
		sudo chmod 755 "$TARGET.new.$$"
		sudo mv -f "$TARGET.new.$$" "$TARGET"
		return 0
	fi
	die "cannot write to $INSTALL_DIR, choose another directory with --dir"
}

path_hint() {
	case "$(basename "${SHELL:-sh}")" in
	zsh) rc="~/.zshrc" ;;
	bash) rc="~/.bashrc" ;;
	fish)
		say "  fish_add_path $INSTALL_DIR"
		return 0
		;;
	*) rc="~/.profile" ;;
	esac
	say "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> $rc"
	say "then open a new terminal."
}

report() {
	if ! installed=$("$TARGET" --version 2>/dev/null); then
		die "the installed binary at $TARGET does not run"
	fi
	say "${GREEN}Installed${RESET} $installed at $TARGET"
	case ":$PATH:" in
	*":$INSTALL_DIR:"*)
		found=$(command -v "$BIN" 2>/dev/null || true)
		if [ -n "$found" ] && [ "$found" != "$TARGET" ]; then
			warn "another $BIN at $found comes first in PATH and will run instead"
		fi
		say "Run it with: ${BOLD}$BIN${RESET}"
		;;
	*)
		warn "$INSTALL_DIR is not on your PATH. Add it with:"
		path_hint
		say "Or run it directly: $TARGET"
		;;
	esac
}

uninstall() {
	if [ ! -e "$TARGET" ] && [ ! -L "$TARGET" ]; then
		say "$BIN is not installed in $INSTALL_DIR"
		return 0
	fi
	if rm -f "$TARGET" 2>/dev/null; then
		:
	elif [ "$(id -u)" != 0 ] && has sudo; then
		sudo rm -f "$TARGET"
	else
		die "cannot remove $TARGET"
	fi
	say "Removed $TARGET"
}

main() {
	if [ -f "$0" ]; then
		SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" 2>/dev/null && pwd) || SCRIPT_DIR=""
	fi
	parse_args "$@"
	detect_platform

	if [ "$UNINSTALL" = 1 ]; then
		uninstall
		exit 0
	fi

	TMP=$(mktemp -d 2>/dev/null || mktemp -d -t netspeed)
	trap 'rm -rf "$TMP"' EXIT
	trap 'exit 130' INT TERM

	if [ "$FROM_SOURCE" = 1 ]; then
		build_source
	elif ! download_release; then
		warn "no prebuilt binary found for $VERSION, building from source instead"
		build_source
	fi

	place "$BUILT"
	report
}

main "$@"
