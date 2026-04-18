#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${DIST_DIR:-${ROOT_DIR}/dist}"
VERSION="${VERSION:-dev}"
TARGET_OSES="${TARGET_OSES:-linux freebsd darwin}"
TARGET_ARCHES="${TARGET_ARCHES:-amd64 arm64}"
EPOCH="${SOURCE_DATE_EPOCH:-0}"

mkdir -p "${DIST_DIR}/artifacts"
rm -f "${DIST_DIR}/artifacts"/*

for goos in ${TARGET_OSES}; do
	for goarch in ${TARGET_ARCHES}; do
		bin_name="s000"
		ext=""
		if [[ "${goos}" == "windows" ]]; then
			ext=".exe"
			bin_name="s000.exe"
		fi

		stage_dir="${DIST_DIR}/stage/s000-${VERSION}-${goos}-${goarch}"
		rm -rf "${stage_dir}"
		mkdir -p "${stage_dir}"

		CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
			go build -trimpath -ldflags='-s -w -buildid=' -o "${stage_dir}/${bin_name}" ./cmd/s000

		cp "${ROOT_DIR}/README.md" "${stage_dir}/README.md"
		cp "${ROOT_DIR}/SPEC.md" "${stage_dir}/SPEC.md"

		archive="${DIST_DIR}/artifacts/s000-${VERSION}-${goos}-${goarch}.tar.gz"
		tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@${EPOCH}" \
			-C "${DIST_DIR}/stage" -czf "${archive}" "s000-${VERSION}-${goos}-${goarch}"
	done
done

( cd "${DIST_DIR}/artifacts" && sha256sum ./*.tar.gz > checksums.txt )

echo "release artifacts written to ${DIST_DIR}/artifacts"
