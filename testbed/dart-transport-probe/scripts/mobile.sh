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
if [[ ${LANTERN_PROBE_DRIVER:-flutter-test} == simctl ]]; then
  cp "$probe_root/templates/$transport/mobile_main.dart" "$app/lib/main.dart"
else
  cp "$probe_root/templates/main.dart" "$app/lib/main.dart"
  mkdir -p "$app/integration_test"
  cp "$probe_root/templates/$transport/probe_test.dart" \
    "$app/integration_test/probe_test.dart"
fi

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

ca_der="$work_root/lantern-probe-ca.der"
openssl x509 -in "$ca_cert" -outform der -out "$ca_der"
ca_base64=$(base64 < "$ca_der" | tr -d '\n')
ca_pem_base64=$(base64 < "$ca_cert" | tr -d '\n')
leaf_base64=
if [[ -n ${LANTERN_PROBE_SERVER_CERT:-} ]]; then
  leaf_base64=$(base64 < "$LANTERN_PROBE_SERVER_CERT" | tr -d '\n')
fi
cd "$app"
flutter pub get
dart_defines=(
  --dart-define="LANTERN_PROBE_PLAINTEXT_URL=$plaintext_url"
  --dart-define="LANTERN_PROBE_TLS_URL=$tls_url"
  --dart-define="LANTERN_PROBE_TOKEN=$token"
  --dart-define="LANTERN_PROBE_CA_BASE64=$ca_base64"
  --dart-define="LANTERN_PROBE_CA_PEM_BASE64=$ca_pem_base64"
  --dart-define="LANTERN_PROBE_LEAF_BASE64=$leaf_base64"
)

if [[ ${LANTERN_PROBE_DRIVER:-flutter-test} == simctl ]]; then
  flutter build ios --simulator --debug "${dart_defines[@]}"
  ios_app=build/ios/iphonesimulator/Runner.app
  bundle_id=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' \
    "$ios_app/Info.plist")
  xcrun simctl install "$device" "$ios_app"
  xcrun simctl launch --terminate-running-process "$device" "$bundle_id"

  count_url="${tls_url%/}/graph.v1.LanternService/CountVerticesByPrefix"
  success_prefix="probe/$transport/ios-success/"
  for _ in {1..120}; do
    response=$(curl --silent --show-error --cacert "$ca_cert" \
      --header "Authorization: Bearer $token" \
      --header 'Connect-Protocol-Version: 1' \
      --header 'Content-Type: application/json' \
      --data "{\"prefix\":\"$success_prefix\"}" "$count_url" || true)
    if jq -e '(.count | tonumber) > 0' <<<"$response" >/dev/null 2>&1; then
      echo "$transport iOS real-wire probe passed"
      break
    fi
    sleep 1
  done
  if ! jq -e '(.count | tonumber) > 0' <<<"${response:-}" >/dev/null 2>&1; then
    xcrun simctl spawn "$device" log show --last 5m \
      --predicate 'process == "Runner"' || true
    echo "$transport iOS real-wire probe did not publish its success marker" >&2
    exit 1
  fi
else
  flutter test integration_test/probe_test.dart \
    -d "$device" \
    --reporter=expanded \
    --timeout=2m \
    "${dart_defines[@]}"
fi

if [[ -f build/app/outputs/flutter-apk/app-debug.apk ]]; then
  wc -c build/app/outputs/flutter-apk/app-debug.apk
fi
if [[ -d build/ios/iphonesimulator/Runner.app ]]; then
  du -sk build/ios/iphonesimulator/Runner.app
fi
