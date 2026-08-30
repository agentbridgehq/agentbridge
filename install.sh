#!/bin/sh
# Install agentbridge.
#
#   curl -fsSL https://raw.githubusercontent.com/agentbridge/agentbridge/main/install.sh | sh
#
# Checksum verification is mandatory and cannot be turned off. The usual
# curl-pipe-to-shell installer downloads a binary and runs it without checking
# anything, which is precisely the supply-chain posture this project exists to
# argue against; an installer that skipped its own verification would undermine
# every claim the tool makes.
#
# Signature verification runs whenever cosign is present. Set
# AGENTBRIDGE_REQUIRE_SIGNATURE=1 to make its absence an error instead — the
# right setting for CI and for any managed fleet.
#
# Environment:
#   AGENTBRIDGE_VERSION              version to install (default: latest)
#   AGENTBRIDGE_BINDIR               install directory (default: /usr/local/bin,
#                                    or ~/.local/bin when that is not writable)
#   AGENTBRIDGE_REQUIRE_SIGNATURE    1 to fail when cosign is unavailable
#   AGENTBRIDGE_BASE_URL             override the download location (testing)
set -eu

REPO="agentbridgehq/agentbridge"
IDENTITY_REGEXP="https://github.com/${REPO}/.github/workflows/release.yml@.*"
OIDC_ISSUER="https://token.actions.githubusercontent.com"

log()  { printf '%s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

need() {
    command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"
}

detect_os() {
    case "$(uname -s)" in
        Darwin) echo darwin ;;
        Linux)  echo linux ;;
        *)      die "unsupported operating system: $(uname -s). Download a release manually from https://github.com/${REPO}/releases" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)  echo amd64 ;;
        arm64|aarch64) echo arm64 ;;
        *)             die "unsupported architecture: $(uname -m)" ;;
    esac
}

latest_version() {
    # The redirect from /releases/latest names the tag, which avoids depending
    # on the API and its rate limits.
    resolved=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest") || return 0
    # Only a URL that actually redirected to a tag carries a version. Without
    # this check a failed lookup returns the request URL unchanged, sed leaves
    # it alone, and the whole URL is then used as the version — which builds a
    # download address containing three copies of itself and reports the
    # confusion as a download failure rather than as a lookup failure.
    case "$resolved" in
        */tag/*) printf '%s\n' "${resolved##*/tag/}" ;;
        *)       return 0 ;;
    esac
}

# verify_checksum confirms one file against a checksums.txt listing.
#
# --ignore-missing is deliberately not used: it would let a checksums file that
# does not mention our artifact pass silently, which is the failure this step
# exists to catch.
verify_checksum() {
    archive="$1"
    checksums="$2"

    expected=$(grep " $(basename "$archive")\$" "$checksums" | awk '{print $1}')
    [ -n "$expected" ] || die "checksums.txt does not list $(basename "$archive"); refusing to install"

    if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum "$archive" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
        actual=$(shasum -a 256 "$archive" | awk '{print $1}')
    else
        die "no sha256 tool found (looked for sha256sum and shasum); refusing to install unverified"
    fi

    [ "$expected" = "$actual" ] || die "checksum mismatch for $(basename "$archive")
  expected $expected
  actual   $actual
Do not use this download."

    log "  checksum  ok"
}

verify_signature() {
    checksums="$1"
    sig="$2"
    cert="$3"

    if ! command -v cosign >/dev/null 2>&1; then
        if [ "${AGENTBRIDGE_REQUIRE_SIGNATURE:-0}" = "1" ]; then
            die "AGENTBRIDGE_REQUIRE_SIGNATURE=1 but cosign is not installed"
        fi
        log "  signature not checked (cosign is not installed)"
        log "            install cosign, or set AGENTBRIDGE_REQUIRE_SIGNATURE=1 to require it"
        return 0
    fi

    # Identity is pinned to the release workflow in this repository. Verifying
    # a signature without pinning the identity only proves somebody signed it.
    cosign verify-blob "$checksums" \
        --signature "$sig" \
        --certificate "$cert" \
        --certificate-identity-regexp "$IDENTITY_REGEXP" \
        --certificate-oidc-issuer "$OIDC_ISSUER" >/dev/null 2>&1 \
        || die "signature verification failed for checksums.txt. Do not use this download."

    log "  signature ok (signed by ${REPO} release workflow)"
}

choose_bindir() {
    if [ -n "${AGENTBRIDGE_BINDIR:-}" ]; then
        echo "$AGENTBRIDGE_BINDIR"
        return
    fi
    if [ -w /usr/local/bin ] 2>/dev/null; then
        echo /usr/local/bin
    else
        echo "$HOME/.local/bin"
    fi
}

main() {
    need curl
    need tar

    os=$(detect_os)
    arch=$(detect_arch)

    version="${AGENTBRIDGE_VERSION:-}"
    if [ -z "$version" ]; then
        version=$(latest_version)
        [ -n "$version" ] || die "could not determine the latest version"
    fi
    stripped="${version#v}"

    base="${AGENTBRIDGE_BASE_URL:-https://github.com/${REPO}/releases/download/${version}}"
    archive="agentbridge_${stripped}_${os}_${arch}.tar.gz"

    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT INT TERM

    log "agentbridge ${version} (${os}/${arch})"

    curl -fsSL "${base}/${archive}"           -o "${tmp}/${archive}"      || die "download failed: ${base}/${archive}"
    curl -fsSL "${base}/checksums.txt"        -o "${tmp}/checksums.txt"   || die "could not download checksums.txt; refusing to install unverified"
    curl -fsSL "${base}/checksums.txt.sig"    -o "${tmp}/checksums.txt.sig"  2>/dev/null || true
    curl -fsSL "${base}/checksums.txt.pem"    -o "${tmp}/checksums.txt.pem"  2>/dev/null || true

    verify_checksum "${tmp}/${archive}" "${tmp}/checksums.txt"

    if [ -s "${tmp}/checksums.txt.sig" ] && [ -s "${tmp}/checksums.txt.pem" ]; then
        verify_signature "${tmp}/checksums.txt" "${tmp}/checksums.txt.sig" "${tmp}/checksums.txt.pem"
    elif [ "${AGENTBRIDGE_REQUIRE_SIGNATURE:-0}" = "1" ]; then
        die "AGENTBRIDGE_REQUIRE_SIGNATURE=1 but this release publishes no signature"
    else
        log "  signature not published for this release"
    fi

    # Extract from inside the directory rather than with -C after -f: BSD tar
    # applies options in order, so a -C that follows -f changes the directory
    # before the archive path is resolved and the open fails.
    (cd "$tmp" && tar -xzf "$archive") || die "could not extract ${archive}"
    [ -f "${tmp}/agentbridge" ] || die "the archive does not contain an agentbridge binary"

    bindir=$(choose_bindir)
    mkdir -p "$bindir"
    install -m 0755 "${tmp}/agentbridge" "${bindir}/agentbridge" 2>/dev/null \
        || { cp "${tmp}/agentbridge" "${bindir}/agentbridge" && chmod 0755 "${bindir}/agentbridge"; }

    log "  installed ${bindir}/agentbridge"

    case ":${PATH}:" in
        *":${bindir}:"*) ;;
        *) log ""
           log "${bindir} is not on your PATH. Add it:"
           log "  export PATH=\"${bindir}:\$PATH\"" ;;
    esac
}

main "$@"
