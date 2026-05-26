#!/usr/bin/env bash

# Install hanko from a GitHub release.

set -euo pipefail

REPO="dmallubhotla/hanko"

usage() {
  cat <<-EOF
		Usage: $(basename "${BASH_SOURCE[0]}") [options]

		Downloads the hanko binary for the current OS/arch from the GitHub
		releases page, verifies it against the published checksums, and
		installs it to a directory on your PATH.

		Supported targets: linux-amd64, linux-arm64.

		Available options:
		-h, --help                Print help
		-v, --verbose             Be a bit more verbose
		-V, --version TAG         Install a specific release (e.g. v0.2.3). Default: latest.
		-d, --install-dir DIR     Where to drop the binary. Default: \$HOME/.local/bin.

		Environment overrides (same effect as the flags above):
		HANKO_VERSION, HANKO_INSTALL_DIR
	EOF
  exit 0
}

fail() {
  echo >&2 "ERROR: ${1}"
  exit 1
}

log() {
  echo "$1"
}

parse_args() {
  _verbose=0
  _version="${HANKO_VERSION:-}"
  _install_dir="${HANKO_INSTALL_DIR:-$HOME/.local/bin}"

  while :; do
    case "${1-}" in
    -h | --help)
      usage
      ;;
    -v | --verbose)
      set -x
      _verbose=1
      ;;
    -V | --version)
      _version="${2-}"
      shift
      ;;
    -d | --install-dir)
      _install_dir="${2-}"
      shift
      ;;
    "")
      break
      ;;
    *)
      fail "Unknown argument: $1 (try --help)"
      ;;
    esac
    shift
  done
}

detect_target() {
  local os arch os_tag arch_tag
  os="$(uname -s)"
  arch="$(uname -m)"

  case "${os}" in
  Linux) os_tag='linux' ;;
  *) fail "unsupported OS: ${os} (hanko ships linux builds only)" ;;
  esac

  case "${arch}" in
  x86_64 | amd64) arch_tag='amd64' ;;
  arm64 | aarch64) arch_tag='arm64' ;;
  *) fail "unsupported architecture: ${arch}" ;;
  esac

  _target="${os_tag}-${arch_tag}"
  case "${_target}" in
  linux-amd64 | linux-arm64) ;;
  *) fail "no prebuilt binary for ${_target} (supported: linux-amd64, linux-arm64)" ;;
  esac
}

resolve_release_url() {
  if [[ -n ${_version} ]]; then
    _release_url="https://github.com/${REPO}/releases/download/${_version}"
    _version_label="${_version}"
  else
    _release_url="https://github.com/${REPO}/releases/latest/download"
    _version_label='latest'
  fi
}

fetch() {
  local src="$1" dst="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "${src}" -o "${dst}"
  else
    wget -q "${src}" -O "${dst}"
  fi
}

verify_checksum() {
  local file="$1" sums="$2" line
  line="$(grep "  $(basename "${file}")\$" "${sums}" || true)"
  [[ -n ${line} ]] || fail "no checksum entry for $(basename "${file}") in checksums.txt"
  (cd "$(dirname "${file}")" && printf '%s\n' "${line}" | ${_checksum_cmd} -c - >/dev/null) ||
    fail "checksum verification failed for $(basename "${file}")"
}

parse_args "$@"

command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 ||
  fail "curl or wget is required"

if command -v sha256sum >/dev/null 2>&1; then
  _checksum_cmd='sha256sum'
elif command -v shasum >/dev/null 2>&1; then
  _checksum_cmd='shasum -a 256'
else
  fail "sha256sum or shasum is required"
fi

detect_target
resolve_release_url

if [[ ${_verbose} -eq 1 ]]; then
  log "target:       ${_target}"
  log "version:      ${_version_label}"
  log "install dir:  ${_install_dir}"
fi

_tmpdir="$(mktemp -d)"
trap 'rm -rf "${_tmpdir}"' EXIT

log "Fetching hanko (${_version_label}) for ${_target}..."
fetch "${_release_url}/hanko-${_target}" "${_tmpdir}/hanko-${_target}"
fetch "${_release_url}/checksums.txt" "${_tmpdir}/checksums.txt"

log "Verifying checksum..."
verify_checksum "${_tmpdir}/hanko-${_target}" "${_tmpdir}/checksums.txt"

mkdir -p "${_install_dir}"
chmod +x "${_tmpdir}/hanko-${_target}"
mv -f "${_tmpdir}/hanko-${_target}" "${_install_dir}/hanko"
log "Installed ${_install_dir}/hanko"

case ":${PATH}:" in
*":${_install_dir}:"*) ;;
*)
  log ""
  log "NOTE: ${_install_dir} is not on your PATH. Add this to your shell rc:"
  log "  export PATH=\"${_install_dir}:\$PATH\""
  ;;
esac

log "Done. Try: hanko version"
