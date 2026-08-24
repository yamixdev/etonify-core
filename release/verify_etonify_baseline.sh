#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repository_root}"

# shellcheck disable=SC1091
source release/ETONIFY_BASELINE

etonify_version="$(tr -d '\r\n' < release/ETONIFY_VERSION)"
etonify_version_prefix="${UPSTREAM_TAG}-etonify."
etonify_version_suffix="${etonify_version#"${etonify_version_prefix}"}"
if [[ "${etonify_version}" != "${etonify_version_prefix}"* || ! "${etonify_version_suffix}" =~ ^[0-9]+$ ]]; then
  printf 'invalid Etonify version: %s (expected %s-etonify.N)\n' "${etonify_version}" "${UPSTREAM_TAG}" >&2
  exit 1
fi

git cat-file -e "${UPSTREAM_COMMIT}^{commit}"
git merge-base --is-ancestor "${UPSTREAM_COMMIT}" HEAD

grep -Fq "github.com/sagernet/gomobile/cmd/gomobile@${GOMOBILE_VERSION}" Makefile
grep -Fq "github.com/sagernet/gomobile/cmd/gobind@${GOMOBILE_VERSION}" Makefile
grep -Fq "AndroidAPI: ${LIBBOX_ANDROID_API}" cmd/internal/build_libbox/main.go
for build_tag in ${LIBBOX_BUILD_TAGS//,/ }; do
  grep -Fq "\"${build_tag}\"" cmd/internal/build_libbox/main.go
done
for excluded_tag in with_usbip with_openvpn with_openconnect; do
  if grep -Fq "\"${excluded_tag}\"" cmd/internal/build_libbox/main.go; then
    printf 'unexpected Android build tag: %s\n' "${excluded_tag}" >&2
    exit 1
  fi
done

printf 'upstream=%s\n' "${UPSTREAM_COMMIT}"
printf 'etonify=%s\n' "${etonify_version}"
printf 'go=%s\n' "${GO_VERSION}"
printf 'gomobile=%s\n' "${GOMOBILE_VERSION}"
printf 'ndk=%s\n' "${ANDROID_NDK_VERSION}"
printf 'java=%s\n' "${JAVA_VERSION}"
printf 'android_api=%s\n' "${LIBBOX_ANDROID_API}"
printf 'build_tags=%s\n' "${LIBBOX_BUILD_TAGS}"
