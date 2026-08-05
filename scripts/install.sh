#!/bin/sh

set -eu

repository="${RTOP_REPO:-zouri/rtop}"
requested_version="${RTOP_VERSION:-latest}"

fail() {
    printf 'rtop installer: %s\n' "$*" >&2
    exit 1
}

command_exists() {
    command -v "$1" >/dev/null 2>&1
}

download() {
    url="$1"
    destination="$2"

    if command_exists curl; then
        curl -fsSL --retry 3 --connect-timeout 10 -o "$destination" "$url"
    elif command_exists wget; then
        wget -q --tries=3 --timeout=10 -O "$destination" "$url"
    else
        fail "curl or wget is required"
    fi
}

resolve_version() {
    if [ "$requested_version" != "latest" ]; then
        case "$requested_version" in
            v*) printf '%s\n' "$requested_version" ;;
            *) printf 'v%s\n' "$requested_version" ;;
        esac
        return
    fi

    metadata="$temporary_directory/release.json"
    download "https://api.github.com/repos/$repository/releases/latest" "$metadata"
    version=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$metadata" | head -n 1)
    [ -n "$version" ] || fail "could not determine the latest release for $repository"
    printf '%s\n' "$version"
}

verify_checksum() {
    checksum_file="$1"
    archive_path="$2"
    archive_name="$3"
    expected=$(awk -v name="$archive_name" '$2 == name { print $1; exit }' "$checksum_file")
    [ -n "$expected" ] || fail "SHA256SUMS does not contain $archive_name"

    if command_exists sha256sum; then
        actual=$(sha256sum "$archive_path" | awk '{ print $1 }')
    elif command_exists shasum; then
        actual=$(shasum -a 256 "$archive_path" | awk '{ print $1 }')
    else
        fail "sha256sum or shasum is required to verify the download"
    fi

    [ "$actual" = "$expected" ] || fail "checksum verification failed for $archive_name"
}

[ "$(uname -s)" = "Linux" ] || fail "only Linux is currently supported"

case "$(uname -m)" in
    aarch64|arm64) ;;
    *) fail "only ARM64 is currently supported" ;;
esac

command_exists tar || fail "tar is required"
temporary_directory=$(mktemp -d "${TMPDIR:-/tmp}/rtop-install.XXXXXX")
trap 'rm -rf -- "$temporary_directory"' EXIT HUP INT TERM

version=$(resolve_version)
archive_name="rtop-$version-linux-arm64.tar.gz"
release_url="https://github.com/$repository/releases/download/$version"
archive_path="$temporary_directory/$archive_name"
checksum_path="$temporary_directory/SHA256SUMS"

printf 'Downloading rtop %s for Linux ARM64...\n' "$version"
download "$release_url/$archive_name" "$archive_path"
download "$release_url/SHA256SUMS" "$checksum_path"
verify_checksum "$checksum_path" "$archive_path" "$archive_name"

tar -xzf "$archive_path" -C "$temporary_directory"
binary="$temporary_directory/rtop-$version-linux-arm64/rtop"
[ -f "$binary" ] || fail "the release archive does not contain rtop"

if [ -n "${RTOP_INSTALL_DIR:-}" ]; then
    install_directory="$RTOP_INSTALL_DIR"
elif [ "$(id -u)" -eq 0 ] || [ -w /usr/local/bin ]; then
    install_directory=/usr/local/bin
else
    [ -n "${HOME:-}" ] || fail "HOME is not set; specify RTOP_INSTALL_DIR"
    install_directory="${XDG_BIN_HOME:-$HOME/.local/bin}"
fi

mkdir -p "$install_directory"
cp "$binary" "$install_directory/rtop"
chmod 0755 "$install_directory/rtop"

printf 'Installed rtop %s to %s/rtop\n' "$version" "$install_directory"
case ":${PATH:-}:" in
    *":$install_directory:"*) ;;
    *) printf 'Add %s to PATH to run rtop from any directory.\n' "$install_directory" ;;
esac
