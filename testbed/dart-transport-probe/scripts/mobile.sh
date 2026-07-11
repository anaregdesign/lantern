#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 6 ]]; then
  echo "usage: $0 <connect|grpc> <device-id> <plaintext-url> <tls-url> <bearer-token> <ca-cert>" >&2
  exit 64
fi

transport=$1
device=$2
plaintext_url=$3
tls_url=$4
token=$5
ca_cert=$6
probe_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

case "$transport" in
  connect)
    package_name=lantern_connect_transport_probe
    ;;
  grpc)
    package_name=lantern_grpc_transport_probe
    ;;
  *)
    echo "unsupported transport: $transport" >&2
    exit 64
    ;;
esac

project_name="lantern_${transport}_transport_mobile_probe"
work_root=${LANTERN_PROBE_WORKDIR:-$(mktemp -d)}
app="$work_root/$project_name"
mkdir -p "$work_root"
if [[ -z ${LANTERN_PROBE_WORKDIR:-} ]]; then
  trap 'rm -rf "$work_root"' EXIT
fi

flutter create \
  --platforms=android,ios \
  --org=com.anaregdesign \
  --project-name="$project_name" \
  "$app"

package_path="$probe_root/$transport"
sed \
  -e "s|__PROJECT_NAME__|$project_name|g" \
  -e "s|__PACKAGE_NAME__|$package_name|g" \
  -e "s|__PACKAGE_PATH__|$package_path|g" \
  "$probe_root/templates/pubspec.yaml" > "$app/pubspec.yaml"
cp "$probe_root/templates/main.dart" "$app/lib/main.dart"
mkdir -p "$app/integration_test"
cp "$probe_root/templates/$transport/probe_test.dart" \
  "$app/integration_test/probe_test.dart"

manifest="$app/android/app/src/main/AndroidManifest.xml"
perl -0pi -e \
  's/(android:name="\$\{applicationName\}"\n)/$1        android:usesCleartextTraffic="true"\n/' \
  "$manifest"

plist="$app/ios/Runner/Info.plist"
if [[ -x /usr/libexec/PlistBuddy ]]; then
  /usr/libexec/PlistBuddy -c 'Add :NSAppTransportSecurity dict' "$plist" \
    2>/dev/null || true
  /usr/libexec/PlistBuddy \
    -c 'Add :NSAppTransportSecurity:NSAllowsLocalNetworking bool true' \
    "$plist" 2>/dev/null || \
    /usr/libexec/PlistBuddy \
      -c 'Set :NSAppTransportSecurity:NSAllowsLocalNetworking true' "$plist"
fi

ca_base64=$(base64 < "$ca_cert" | tr -d '\n')
cd "$app"
flutter pub get
flutter test integration_test/probe_test.dart \
  -d "$device" \
  --reporter=expanded \
  --timeout=2m \
  --dart-define="LANTERN_PROBE_PLAINTEXT_URL=$plaintext_url" \
  --dart-define="LANTERN_PROBE_TLS_URL=$tls_url" \
  --dart-define="LANTERN_PROBE_TOKEN=$token" \
  --dart-define="LANTERN_PROBE_CA_BASE64=$ca_base64"

if [[ -f build/app/outputs/flutter-apk/app-debug.apk ]]; then
  wc -c build/app/outputs/flutter-apk/app-debug.apk
fi
if [[ -d build/ios/iphonesimulator/Runner.app ]]; then
  du -sk build/ios/iphonesimulator/Runner.app
fi
