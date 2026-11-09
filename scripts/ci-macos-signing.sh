#!/usr/bin/env bash
# Keep signing material in a temporary CI keychain, including on failure.
set +x
set -euo pipefail
cd "$(dirname "$0")/.."

usage() { echo 'Usage: scripts/ci-macos-signing.sh run|cleanup' >&2; }
case "${1:-}" in run|cleanup) ;; *) usage; exit 2 ;; esac
[[ "$(uname -s)" == Darwin && "$(uname -m)" == arm64 ]] || { echo 'Apple Silicon macOS is required.' >&2; exit 1; }
[[ "${GITHUB_ACTIONS:-}" == true && -n "${RUNNER_TEMP:-}" && -d "$RUNNER_TEMP" ]] || {
  echo 'This helper is restricted to a GitHub Actions runner.' >&2; exit 1;
}

cleanup() {
  local state="${CAGE_MACOS_SIGNING_STATE:-}" keychain failed=0
  [[ -n "$state" ]] || return 0
  case "$state" in "$RUNNER_TEMP"/cage-macos-signing.*) ;; *) echo 'Refusing an unexpected signing state directory.' >&2; return 1 ;; esac
  [[ -d "$state" && ! -L "$state" ]] || return 0
  [[ -f "$state/owned-by-cage-ci" ]] || { echo 'Signing state marker is missing.' >&2; return 1; }
  keychain="$state/signing.keychain-db"
  if [[ -f "$state/original-keychains" ]]; then
    local originals=() entry
    while IFS= read -r entry; do
      [[ -n "$entry" ]] && originals+=("$entry")
    done < "$state/original-keychains"
    if [[ ${#originals[@]} -gt 0 ]]; then
      security list-keychains -d user -s "${originals[@]}" || failed=1
    else
      security list-keychains -d user -s || failed=1
    fi
  fi
  if [[ -f "$keychain" ]]; then security delete-keychain "$keychain" || failed=1; fi
  rm -f "$state/certificate.p12" "$state/notary.p8" "$state/Cage.provisionprofile"
  # Retain only cleanup state if Security.framework needs the workflow's
  # always() step to retry restoring the search list or deleting the keychain.
  if [[ "$failed" == 0 ]]; then rm -rf "$state"; fi
  return "$failed"
}
if [[ "$1" == cleanup ]]; then cleanup; exit; fi

for required in CAGE_MACOS_P12_BASE64 CAGE_MACOS_P12_PASSWORD CAGE_MACOS_SIGNING_IDENTITY \
  CAGE_MACOS_NOTARY_KEY_BASE64 CAGE_MACOS_NOTARY_KEY_ID CAGE_MACOS_NOTARY_ISSUER_ID; do
  [[ -n "${!required:-}" ]] || { printf 'Missing cage-release secret: %s\n' "$required" >&2; exit 1; }
done
[[ -n "${GITHUB_ENV:-}" ]] || { echo 'GITHUB_ENV is required for the final cleanup step.' >&2; exit 1; }
umask 077
CAGE_MACOS_SIGNING_STATE="$(mktemp -d "$RUNNER_TEMP/cage-macos-signing.XXXXXX")"
export CAGE_MACOS_SIGNING_STATE
: > "$CAGE_MACOS_SIGNING_STATE/owned-by-cage-ci"
finish() {
  local status=$?
  trap - EXIT
  if ! cleanup; then
    echo 'Temporary signing cleanup did not fully complete.' >&2
    [[ "$status" != 0 ]] || status=1
  fi
  exit "$status"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
printf 'CAGE_MACOS_SIGNING_STATE=%s\n' "$CAGE_MACOS_SIGNING_STATE" >> "$GITHUB_ENV"

# Preserve the runner's search list. Never replace its default keychain.
security list-keychains -d user > "$CAGE_MACOS_SIGNING_STATE/original-keychains.raw"
sed -E 's/^[[:space:]]*"(.*)"[[:space:]]*$/\1/' \
  "$CAGE_MACOS_SIGNING_STATE/original-keychains.raw" > "$CAGE_MACOS_SIGNING_STATE/original-keychains"
keychain="$CAGE_MACOS_SIGNING_STATE/signing.keychain-db"
keychain_password="$(openssl rand -hex 32)"
certificate="$CAGE_MACOS_SIGNING_STATE/certificate.p12"
notary_key="$CAGE_MACOS_SIGNING_STATE/notary.p8"
printf '%s' "$CAGE_MACOS_P12_BASE64" | base64 --decode > "$certificate"
printf '%s' "$CAGE_MACOS_NOTARY_KEY_BASE64" | base64 --decode > "$notary_key"
security create-keychain -p "$keychain_password" "$keychain"
security set-keychain-settings -lut 7200 "$keychain"
security unlock-keychain -p "$keychain_password" "$keychain"
security import "$certificate" -P "$CAGE_MACOS_P12_PASSWORD" -t cert -f pkcs12 -k "$keychain" \
  -T /usr/bin/codesign -T /usr/bin/security
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$keychain_password" "$keychain" > /dev/null

original_keychains=()
while IFS= read -r entry; do [[ -n "$entry" ]] && original_keychains+=("$entry"); done \
  < "$CAGE_MACOS_SIGNING_STATE/original-keychains"
if [[ ${#original_keychains[@]} -gt 0 ]]; then
  security list-keychains -d user -s "$keychain" "${original_keychains[@]}"
else
  security list-keychains -d user -s "$keychain"
fi
# Resolve the selected Developer ID certificate inside this keychain only.
# The build receives its fingerprint, so another keychain cannot satisfy it.
identity=''
while IFS= read -r entry; do
  [[ "$entry" == *'"Developer ID Application: '* ]] || continue
  fingerprint="$(printf '%s\n' "$entry" | awk '{ print $2 }')"
  [[ "$fingerprint" =~ ^[[:xdigit:]]{40}$ ]] || continue
  if [[ "$CAGE_MACOS_SIGNING_IDENTITY" == "$fingerprint" || "$entry" == *"\"$CAGE_MACOS_SIGNING_IDENTITY\""* ]]; then
    [[ -z "$identity" ]] || { echo 'More than one Developer ID certificate matches the signing identity.' >&2; exit 1; }
    identity="$fingerprint"
  fi
done < <(security find-identity -v -p codesigning "$keychain")
[[ -n "$identity" ]] || { echo 'The requested Developer ID Application identity is not present in the temporary keychain.' >&2; exit 1; }

export CAGE_SIGNING_IDENTITY="$identity"
export CAGE_NOTARY_PROFILE=Cage-CI
export CAGE_NOTARY_KEYCHAIN="$keychain"
xcrun notarytool store-credentials "$CAGE_NOTARY_PROFILE" --keychain "$keychain" \
  --key "$notary_key" --key-id "$CAGE_MACOS_NOTARY_KEY_ID" --issuer "$CAGE_MACOS_NOTARY_ISSUER_ID"
unset CAGE_PROVISIONING_PROFILE CAGE_BUILD_OUTPUT
if [[ -n "${CAGE_MACOS_PROVISIONING_PROFILE_BASE64:-}" ]]; then
  export CAGE_PROVISIONING_PROFILE="$CAGE_MACOS_SIGNING_STATE/Cage.provisionprofile"
  printf '%s' "$CAGE_MACOS_PROVISIONING_PROFILE_BASE64" | base64 --decode > "$CAGE_PROVISIONING_PROFILE"
fi
rm -f "$certificate" "$notary_key"
unset CAGE_MACOS_P12_BASE64 CAGE_MACOS_P12_PASSWORD CAGE_MACOS_SIGNING_IDENTITY \
  CAGE_MACOS_NOTARY_KEY_BASE64 CAGE_MACOS_NOTARY_KEY_ID CAGE_MACOS_NOTARY_ISSUER_ID \
  CAGE_MACOS_PROVISIONING_PROFILE_BASE64 keychain_password

bash scripts/package-macos.sh
version="$(tr -d '\r\n' < VERSION)"
bash scripts/verify-macos-release.sh "$PWD/_output/releases/$version"
