#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="${VERSION:-dev}"
package_version="${version#v}"
commit="${COMMIT:-none}"
build_date="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
dist_dir="${repo_root}/dist"
archive_name="rtop-${version}-linux-arm64.tar.gz"
deb_name="rtop_${package_version}_arm64.deb"

mkdir -p "${dist_dir}"
work_dir="$(mktemp -d)"
cleanup() {
    rm -rf -- "${work_dir}"
}
trap cleanup EXIT

binary="${work_dir}/rtop"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
    -buildvcs=false \
    -trimpath \
    -ldflags "-s -w -X main.version=${version} -X main.commit=${commit} -X main.date=${build_date}" \
    -o "${binary}" \
    "${repo_root}/cmd/rtop"

archive_root="${work_dir}/rtop-${version}-linux-arm64"
install -d -m 0755 "${archive_root}"
install -m 0755 "${binary}" "${archive_root}/rtop"
install -m 0644 "${repo_root}/README.md" "${archive_root}/README.md"
install -m 0644 "${repo_root}/README_EN.md" "${archive_root}/README_EN.md"
tar -C "${work_dir}" -czf "${dist_dir}/${archive_name}" "rtop-${version}-linux-arm64"

deb_root="${work_dir}/deb"
install -d -m 0755 "${deb_root}/DEBIAN" "${deb_root}/usr/bin" "${deb_root}/usr/share/doc/rtop"
install -m 0755 "${binary}" "${deb_root}/usr/bin/rtop"
install -m 0644 "${repo_root}/README.md" "${deb_root}/usr/share/doc/rtop/README.md"
install -m 0644 "${repo_root}/README_EN.md" "${deb_root}/usr/share/doc/rtop/README_EN.md"
installed_size="$(du -sk "${deb_root}/usr" | awk '{print $1}')"
cat >"${deb_root}/DEBIAN/control" <<EOF
Package: rtop
Version: ${package_version}
Section: utils
Priority: optional
Architecture: arm64
Maintainer: rtop contributors
Installed-Size: ${installed_size}
Description: Read-only terminal system monitor for Rockchip Linux
 rtop displays CPU, memory, thermal, process, Mali GPU, RKNPU and DDR metrics.
EOF
dpkg-deb --root-owner-group --build "${deb_root}" "${dist_dir}/${deb_name}"

(
    cd "${dist_dir}"
    sha256sum "${archive_name}" "${deb_name}" >SHA256SUMS
)

printf 'Created %s\n' "${dist_dir}/${archive_name}"
printf 'Created %s\n' "${dist_dir}/${deb_name}"
printf 'Created %s\n' "${dist_dir}/SHA256SUMS"
