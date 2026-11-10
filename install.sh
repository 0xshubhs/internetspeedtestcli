#!/bin/sh
set -eu

REPO="0xshubhs/internetspeedtestcli"
MODULE="github.com/$REPO"
REPO_URL="${NETSPEED_REPO_URL:-https://github.com/$REPO.git}"
BIN="netspeed"
REF="${NETSPEED_VERSION:-}"
INSTALL_DIR="${NETSPEED_INSTALL_DIR:-}"
LOCAL=0
UNINSTALL=0
TMP=""
SRC=""
GO=""
TEMP_GO=0
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

git_usable() {
	has git || return 1
	if [ "$(uname -s)" = Darwin ] && ! xcode-select -p >/dev/null 2>&1; then
		return 1
	fi
	return 0
}

usage() {
	cat <<EOF
Install netspeed, the terminal internet speed test.

Usage:
  curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh | sh
  curl -fsSL https://raw.githubusercontent.com/$REPO/main/install.sh | sh -s -- [options]
  ./install.sh [options]

Clones $REPO into a temporary folder, compiles netspeed and installs
the binary. If Go 1.26 or newer is not installed, a verified copy of Go is
downloaded into the same temporary folder. Everything temporary (the clone,
the downloaded Go and its caches) is deleted afterwards.

Options:
  -d, --dir DIR       install directory (default: ~/.local/bin, or /usr/local/bin as root)
  -v, --version REF   tag or branch to build, for example v0.1.0 (default: main)
  -l, --local         build the checkout this script is in instead of cloning
  -u, --uninstall     remove netspeed from the install directory
  -h, --help          show this help

Environment:
  NETSPEED_INSTALL_DIR, NETSPEED_VERSION   same as --dir and --version
  NETSPEED_REPO_URL                        clone from another URL, for mirrors

Needs git (or curl/wget) and tar. Go is optional.
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
			REF=$2
			shift 2
			;;
		--version=*)
			REF=${1#*=}
			shift
			;;
		-l | --local)
			LOCAL=1
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

	if [ -z "$INSTALL_DIR" ]; then
		if [ "$(id -u)" = 0 ]; then
			INSTALL_DIR=/usr/local/bin
		else
			INSTALL_DIR="$HOME/.local/bin"
		fi
	fi
}

detect_platform() {
	os=$(uname -s)
	case "$os" in
	Linux) OS=linux ;;
	Darwin) OS=darwin ;;
	FreeBSD) OS=freebsd ;;
	MINGW* | MSYS* | CYGWIN*) OS=windows ;;
	*) OS=$(printf '%s' "$os" | tr '[:upper:]' '[:lower:]') ;;
	esac

	machine=$(uname -m)
	case "$machine" in
	x86_64 | amd64) ARCH=amd64 ;;
	aarch64 | arm64 | armv8*) ARCH=arm64 ;;
	i386 | i486 | i586 | i686 | x86) ARCH=386 ;;
	armv6* | armv7*) ARCH=armv6l ;;
	loongarch64) ARCH=loong64 ;;
	*) ARCH=$machine ;;
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
	TARGET="$INSTALL_DIR/$BIN$EXT"
}

fetch() {
	if has curl; then
		curl -fsSL --retry 2 --connect-timeout 15 -o "$2" "$1"
	elif has wget; then
		wget -q -T 30 -O "$2" "$1"
	else
		die "neither curl nor wget is installed"
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

cleanup() {
	if [ -n "$TMP" ] && [ -d "$TMP" ]; then
		chmod -R u+w "$TMP" 2>/dev/null || true
		rm -rf "$TMP"
	fi
}

get_source() {
	if [ "$LOCAL" = 1 ]; then
		if [ -z "$SCRIPT_DIR" ] || ! grep -qs "^module $MODULE\$" "$SCRIPT_DIR/go.mod"; then
			die "--local only works when running install.sh from a clone of $REPO"
		fi
		SRC=$SCRIPT_DIR
		BUILD_VERSION=$(git -C "$SRC" describe --tags --always --dirty 2>/dev/null || echo dev)
		step "Using local checkout $SRC"
		return 0
	fi

	SRC="$TMP/src"
	label=${REF:-main}
	if git_usable; then
		step "Cloning $REPO ($label)"
		if [ -n "$REF" ]; then
			GIT_TERMINAL_PROMPT=0 git clone --quiet --depth 1 --branch "$REF" "$REPO_URL" "$SRC" ||
				die "could not clone $REPO_URL at $REF (check that the tag or branch exists)"
		else
			GIT_TERMINAL_PROMPT=0 git clone --quiet --depth 1 "$REPO_URL" "$SRC" ||
				die "could not clone $REPO_URL"
		fi
		BUILD_VERSION=$(git -C "$SRC" describe --tags --always 2>/dev/null || echo "$label")
		return 0
	fi

	step "Downloading $REPO source ($label)"
	mkdir -p "$SRC"
	fetch "https://github.com/$REPO/archive/${REF:-HEAD}.tar.gz" "$TMP/src.tar.gz" ||
		die "could not download the source archive for $label"
	tar -xzf "$TMP/src.tar.gz" -C "$SRC" --strip-components=1 || die "could not unpack the source archive"
	BUILD_VERSION=$label
}

version_ge() {
	have=$(printf '%s' "$1" | sed 's/^[^0-9]*//')
	want=$2
	for i in 1 2 3; do
		h=$(printf '%s' "$have" | cut -d. -f"$i" | sed 's/[^0-9].*$//')
		w=$(printf '%s' "$want" | cut -d. -f"$i" | sed 's/[^0-9].*$//')
		h=${h:-0}
		w=${w:-0}
		if [ "$h" -gt "$w" ]; then
			return 0
		fi
		if [ "$h" -lt "$w" ]; then
			return 1
		fi
	done
	return 0
}

ensure_go() {
	need=$(sed -n 's/^go \([0-9][0-9.]*\).*$/\1/p' "$SRC/go.mod" | head -n 1)
	need=${need:-1.26}

	if has go; then
		have=$(go env GOVERSION 2>/dev/null || true)
		if [ -n "$have" ] && version_ge "$have" "$need"; then
			GO=$(command -v go)
			step "Using Go ${have#go} from $GO"
			return 0
		fi
		say "    found ${have:-an old Go}, but netspeed needs Go $need or newer"
	fi

	latest=$(fetch "https://go.dev/VERSION?m=text" /dev/stdout 2>/dev/null | head -n 1) || true
	case "$latest" in
	go[0-9]*) ;;
	*) die "could not look up the current Go release on go.dev" ;;
	esac

	archive="tar.gz"
	if [ "$OS" = windows ]; then
		archive="zip"
		has unzip || die "unzip is needed to unpack Go on Windows"
	fi
	file="$latest.$OS-$ARCH.$archive"

	fetch "https://go.dev/dl/?mode=json" "$TMP/go-releases.json" || die "could not download the Go release index"
	sum=$(awk -v f="\"$file\"" '
		index($0, "\"filename\": " f) { found = 1 }
		found && /"sha256":/ { gsub(/[",]/, "", $2); print $2; exit }
	' "$TMP/go-releases.json")
	if [ -z "$sum" ]; then
		die "Go has no download for $OS/$ARCH ($file); install Go $need or newer yourself and run this again"
	fi

	step "Downloading $latest for $OS/$ARCH into a temporary folder (about 70 MB, deleted afterwards)"
	fetch "https://go.dev/dl/$file" "$TMP/$file" || die "could not download $file"
	actual=$(sha256 "$TMP/$file") || die "no SHA-256 tool (sha256sum, shasum or openssl) to verify the Go download"
	if [ "$actual" != "$sum" ]; then
		die "checksum mismatch for $file (expected $sum, got $actual)"
	fi
	say "    checksum verified"

	if [ "$archive" = zip ]; then
		unzip -q "$TMP/$file" -d "$TMP"
	else
		tar -xzf "$TMP/$file" -C "$TMP"
	fi
	rm -f "$TMP/$file" "$TMP/go-releases.json"
	GO="$TMP/go/bin/go$EXT"
	TEMP_GO=1
}

build() {
	step "Building $BIN $BUILD_VERSION"
	mkdir -p "$TMP/out"
	BUILT="$TMP/out/$BIN$EXT"
	if [ "$TEMP_GO" = 1 ]; then
		(
			cd "$SRC"
			export GOPATH="$TMP/gopath" GOMODCACHE="$TMP/gopath/pkg/mod" GOCACHE="$TMP/gocache"
			export GOTOOLCHAIN=local GOFLAGS=-modcacherw GOTELEMETRY=off CGO_ENABLED=0
			"$GO" build -trimpath -ldflags "-s -w -X main.version=$BUILD_VERSION" -o "$BUILT" ./cmd/netspeed
		) || die "build failed"
	else
		(
			cd "$SRC"
			CGO_ENABLED=0 "$GO" build -trimpath -ldflags "-s -w -X main.version=$BUILD_VERSION" -o "$BUILT" ./cmd/netspeed
		) || die "build failed"
	fi
}

place() {
	if mkdir -p "$INSTALL_DIR" 2>/dev/null && [ -w "$INSTALL_DIR" ]; then
		INSTALL_DIR=$(cd "$INSTALL_DIR" && pwd)
		TARGET="$INSTALL_DIR/$BIN$EXT"
		step "Installing to $TARGET"
		cp "$BUILT" "$TARGET.new.$$"
		chmod 755 "$TARGET.new.$$"
		mv -f "$TARGET.new.$$" "$TARGET"
		return 0
	fi
	if [ "$(id -u)" != 0 ] && has sudo; then
		step "Installing to $TARGET (needs sudo)"
		sudo mkdir -p "$INSTALL_DIR"
		sudo cp "$BUILT" "$TARGET.new.$$"
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

	has tar || die "tar is not installed"
	if [ "$LOCAL" != 1 ] && ! git_usable && ! has curl && ! has wget; then
		die "install git, curl or wget first"
	fi

	TMP=$(mktemp -d 2>/dev/null || mktemp -d -t netspeed)
	trap cleanup EXIT
	trap 'exit 130' INT TERM

	get_source
	ensure_go
	build
	place
	cleanup
	if [ "$TEMP_GO" = 1 ]; then
		step "Removed the build files and the temporary Go"
	else
		step "Removed the build files"
	fi
	report
}

main "$@"
