#!/bin/zsh

set -euo pipefail

script_dir="${0:A:h}"
repo_dir="${script_dir:h}"
apple_dir="$repo_dir/apple"
notices_path="$apple_dir/ThirdPartyNotices.txt"
qa_readme_path="$repo_dir/docs/qa/STRIDE-Native-Media-Local-QA.txt"
output_dir="${1:-$repo_dir/artifacts/macos}"
derived_dir="${STRIDE_DERIVED_DATA_PATH:-$output_dir/DerivedData}"
configuration="${STRIDE_CONFIGURATION:-Release}"
volume_name="STRIDE"
app_path="${STRIDE_APP_PATH:-}"
dmg_sign_identity="${STRIDE_DMG_SIGN_IDENTITY:--}"
app_sign_identity="${STRIDE_APP_SIGN_IDENTITY:--}"
hardened_runtime="${STRIDE_ENABLE_HARDENED_RUNTIME:-}"

if [[ -z "$hardened_runtime" ]]; then
  if [[ "$app_sign_identity" == "-" ]]; then
    # Ad-hoc signatures have no shared Team ID, so hardened library validation
    # rejects Sparkle's embedded framework. Public builds must instead arrive
    # through STRIDE_APP_PATH already Developer ID signed with runtime enabled.
    hardened_runtime="NO"
  else
    hardened_runtime="YES"
  fi
fi

mkdir -p "$output_dir"

if [[ -z "$app_path" ]]; then
  xcodebuild \
    -workspace "$apple_dir/MeetingAssist.xcworkspace" \
    -scheme MeetingAssistMacApp \
    -configuration "$configuration" \
    -destination 'generic/platform=macOS' \
    -derivedDataPath "$derived_dir" \
    CODE_SIGN_STYLE=Manual \
    CODE_SIGN_IDENTITY="$app_sign_identity" \
    ENABLE_HARDENED_RUNTIME="$hardened_runtime" \
    DEVELOPMENT_TEAM= \
    build

  app_path="$derived_dir/Build/Products/$configuration/STRIDE.app"
fi

if [[ ! -d "$app_path" ]]; then
  print -u2 "Expected STRIDE.app at $app_path"
  exit 1
fi

if [[ ! -s "$notices_path" ]]; then
  print -u2 "Expected third-party notices at $notices_path"
  exit 1
fi

if [[ ! -s "$qa_readme_path" ]]; then
  print -u2 "Expected local QA instructions at $qa_readme_path"
  exit 1
fi

codesign --verify --deep --strict --verbose=2 "$app_path"

version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$app_path/Contents/Info.plist" 2>/dev/null || true)"
if [[ -z "$version" || "$version" == *'$('* ]]; then
  version="1.0"
fi

stage_dir="$(mktemp -d "${TMPDIR:-/tmp}/stride-dmg.XXXXXX")"
cleanup() {
  rm -rf "$stage_dir"
}
trap cleanup EXIT

ditto "$app_path" "$stage_dir/STRIDE.app"
ditto "$notices_path" "$stage_dir/ThirdPartyNotices.txt"
ditto "$qa_readme_path" "$stage_dir/Native Room Preview Read Me.txt"
ln -s /Applications "$stage_dir/Applications"

dmg_path="$output_dir/STRIDE-$version.dmg"
hdiutil create \
  -volname "$volume_name" \
  -srcfolder "$stage_dir" \
  -ov \
  -format UDZO \
  "$dmg_path"

codesign --force --sign "$dmg_sign_identity" "$dmg_path"
codesign --verify --strict --verbose=2 "$dmg_path"
hdiutil verify "$dmg_path"

print "$dmg_path"
