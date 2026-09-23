import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

void main() {
  late Directory sandbox;
  late File fakeFlutter;
  late File fakeXcrun;
  late File xcrunCalls;

  setUp(() async {
    sandbox = await Directory.systemTemp.createTemp('lantern-ios-smoke-');
    fakeFlutter = File('${sandbox.path}/flutter');
    fakeXcrun = File('${sandbox.path}/xcrun');
    xcrunCalls = File('${sandbox.path}/xcrun-calls.txt');
    await _writeExecutable(fakeXcrun, '''#!/usr/bin/env bash
set -euo pipefail
printf '%s\\n' "\$*" >> '${xcrunCalls.path}'
if [[ "\$*" == 'simctl list devices available -j' ]]; then
  cat <<'JSON'
{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-18-6":[{"name":"iPhone 16 Pro","udid":"SOURCE-DEVICE","deviceTypeIdentifier":"com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro"}]}}
JSON
elif [[ "\${1:-}" == simctl && "\${2:-}" == create ]]; then
  printf '%s\\n' 'A1B2C3D4-E5F6-47A8-90AB-CDEF12345678'
else
  echo 'Authorization: Bearer top-secret-token https://private.example.test/path'
  echo 'mobile-smoke:private-key-value'
fi
''');
  });

  tearDown(() async {
    if (await sandbox.exists()) await sandbox.delete(recursive: true);
  });

  test('reports success only after body and terminal pass markers', () async {
    await _writeExecutable(fakeFlutter, '''#!/usr/bin/env bash
echo 'Running Xcode build...'
echo 'Xcode build done. 1.0s'
echo 'MOBILE_SMOKE_BODY_STARTED'
echo 'MOBILE_SMOKE_PASS vertices=13'
echo 'All tests passed!'
''');

    final result = await _runAttempt(sandbox, fakeFlutter, fakeXcrun);

    expect(result.exitCode, 0, reason: result.stderr.toString());
    expect(await _classification(sandbox), 'success');
  });

  test('bounds a post-build launch stall and redacts diagnostics', () async {
    final githubOutput = File('${sandbox.path}/github-output');
    await _writeExecutable(fakeFlutter, '''#!/usr/bin/env bash
echo 'Running Xcode build...'
echo 'Xcode build done. 1.0s'
echo 'Authorization: Bearer flutter-secret https://flutter.private.test/path'
echo 'mobile-smoke:flutter-private-value'
sleep 30
''');
    final stopwatch = Stopwatch()..start();

    final result = await _runAttempt(
      sandbox,
      fakeFlutter,
      fakeXcrun,
      extraEnvironment: {'GITHUB_OUTPUT': githubOutput.path},
    );

    stopwatch.stop();
    expect(result.exitCode, isNonZero);
    expect(await _classification(sandbox), 'launch_stall');
    expect(
      await githubOutput.readAsString(),
      contains('classification=launch_stall\n'),
    );
    expect(stopwatch.elapsed, lessThan(const Duration(seconds: 12)));
    final diagnostics = await _diagnosticText(sandbox);
    expect(diagnostics, isNot(contains('top-secret-token')));
    expect(diagnostics, isNot(contains('private.example.test')));
    expect(diagnostics, isNot(contains('private-key-value')));
    expect(diagnostics, isNot(contains('flutter-secret')));
    expect(diagnostics, isNot(contains('flutter.private.test')));
    expect(diagnostics, isNot(contains('flutter-private-value')));
    expect(diagnostics, contains('<redacted>'));
    expect(diagnostics, contains('<redacted-url>'));
    for (final file in await _diagnosticFiles(sandbox)) {
      expect(
        await file.length(),
        lessThanOrEqualTo(262144),
        reason: '${file.path} exceeded the per-file diagnostic bound',
      );
    }
  });

  test(
    'does not classify a test-body assertion failure as retryable',
    () async {
      await _writeExecutable(fakeFlutter, '''#!/usr/bin/env bash
echo 'Running Xcode build...'
echo 'Xcode build done. 1.0s'
echo 'MOBILE_SMOKE_BODY_STARTED'
echo 'Expected: true Actual: false'
exit 1
''');

      final result = await _runAttempt(sandbox, fakeFlutter, fakeXcrun);

      expect(result.exitCode, isNonZero);
      expect(await _classification(sandbox), 'test_failure');
    },
  );

  test('does not retry a launch failure that exits during capture', () async {
    final fakePs = File('${sandbox.path}/ps');
    await _writeExecutable(fakeFlutter, '''#!/usr/bin/env bash
echo 'Running Xcode build...'
echo 'Xcode build done. 1.0s'
sleep 2
exit 1
''');
    await _writeExecutable(fakePs, '''#!/usr/bin/env bash
sleep 2
echo 'bounded process snapshot'
''');

    final result = await _runAttempt(
      sandbox,
      fakeFlutter,
      fakeXcrun,
      extraEnvironment: {'IOS_SMOKE_PS_BIN': fakePs.path},
    );

    expect(result.exitCode, isNonZero);
    expect(await _classification(sandbox), 'launch_failure');
  });

  test('creates a fresh retry device with the same runtime and type', () async {
    final output = File('${sandbox.path}/retry-device-id');
    final diagnostics = Directory('${sandbox.path}/diagnostics');
    final result = await Process.run(
      'bash',
      [
        _scriptPath,
        'create-retry-device',
        'SOURCE-DEVICE',
        output.path,
        diagnostics.path,
      ],
      workingDirectory: Directory.current.path,
      environment: {
        ...Platform.environment,
        'IOS_SMOKE_XCRUN_BIN': fakeXcrun.path,
        'GITHUB_RUN_ID': 'local',
        'GITHUB_RUN_ATTEMPT': '1',
      },
    );

    expect(result.exitCode, 0, reason: result.stderr.toString());
    expect(
      (await output.readAsString()).trim(),
      'A1B2C3D4-E5F6-47A8-90AB-CDEF12345678',
    );
    final calls = await xcrunCalls.readAsString();
    expect(
      calls,
      contains(
        'simctl create Lantern CI retry local-1 '
        'com.apple.CoreSimulator.SimDeviceType.iPhone-16-Pro '
        'com.apple.CoreSimulator.SimRuntime.iOS-18-6',
      ),
    );
    expect(calls, contains('simctl boot A1B2C3D4-E5F6-47A8-90AB-CDEF12345678'));
    expect(
      calls,
      contains('simctl bootstatus A1B2C3D4-E5F6-47A8-90AB-CDEF12345678 -b'),
    );
  });

  test('finalizes an interrupted raw log before artifact upload', () async {
    final diagnostics = Directory('${sandbox.path}/interrupted');
    await diagnostics.create();
    final flutterLog = File('${diagnostics.path}/flutter.log');
    await flutterLog.writeAsString(
      '${'x' * 300000}\n'
      'Authorization: Bearer interrupted-secret '
      'https://interrupted.private.test/path\n',
    );
    await File('${diagnostics.path}/partial.raw').writeAsString('raw-secret');

    final result = await Process.run('bash', [
      _scriptPath,
      'finalize-diagnostics',
      diagnostics.path,
    ], workingDirectory: Directory.current.path);

    expect(result.exitCode, 0, reason: result.stderr.toString());
    expect(await flutterLog.length(), lessThanOrEqualTo(262144));
    final text = await flutterLog.readAsString();
    expect(text, isNot(contains('interrupted-secret')));
    expect(text, isNot(contains('interrupted.private.test')));
    expect(text, contains('<redacted>'));
    expect(text, contains('<redacted-url>'));
    expect(File('${diagnostics.path}/partial.raw').existsSync(), isFalse);
    expect(
      await File('${diagnostics.path}/finalized.txt').readAsString(),
      'bounded=true redacted=true\n',
    );
  });

  for (final outcome in ['success', 'assertion failure', 'RPC failure']) {
    test(
      'allows a delayed body to report $outcome within the launch budget',
      () async {
        final succeeds = outcome == 'success';
        final bodyOutput = switch (outcome) {
          'success' => 'MOBILE_SMOKE_PASS vertices=13\nAll tests passed!',
          'assertion failure' => 'Expected: true Actual: false',
          _ => 'RPC failed: unavailable',
        };
        await _writeExecutable(fakeFlutter, '''#!/usr/bin/env bash
echo 'Running Xcode build...'
echo 'Xcode build done. 1.0s'
# The fixture normally allows one second after the build; this needs longer.
sleep 3
echo 'MOBILE_SMOKE_BODY_STARTED'
echo '$bodyOutput'
exit ${succeeds ? 0 : 1}
''');

        final result = await _runAttempt(
          sandbox,
          fakeFlutter,
          fakeXcrun,
          extraEnvironment: {
            'IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS': '4',
            'IOS_SMOKE_TOTAL_TIMEOUT_SECONDS': '8',
          },
        );

        expect(
          result.exitCode,
          succeeds ? 0 : isNonZero,
          reason: '${result.stderr}',
        );
        expect(
          await _classification(sandbox),
          succeeds ? 'success' : 'test_failure',
        );
        expect(result.stdout.toString(), contains('MOBILE_SMOKE_BODY_STARTED'));
        expect(
          File(
            '${sandbox.path}/diagnostics/initial/diagnostics/process-tree-before-stop.txt',
          ).existsSync(),
          isFalse,
          reason:
              'A body starting within its launch allowance must not trigger stop diagnostics',
        );
      },
    );
  }

  test(
    'the total deadline stops a launch before its longer allowance expires',
    () async {
      await _writeExecutable(fakeFlutter, '''#!/usr/bin/env bash
echo 'Running Xcode build...'
echo 'Xcode build done. 1.0s'
sleep 30
''');
      final stopwatch = Stopwatch()..start();

      final result = await _runAttempt(
        sandbox,
        fakeFlutter,
        fakeXcrun,
        extraEnvironment: {
          'IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS': '20',
          'IOS_SMOKE_TOTAL_TIMEOUT_SECONDS': '2',
        },
      );

      stopwatch.stop();
      expect(result.exitCode, 71, reason: '${result.stderr}');
      expect(await _classification(sandbox), 'launch_stall');
      expect(stopwatch.elapsed, lessThan(const Duration(seconds: 12)));
      expect(
        result.stdout.toString(),
        isNot(contains('MOBILE_SMOKE_BODY_STARTED')),
      );
    },
  );

  test(
    'a longer launch allowance does not reclassify a stalled test body',
    () async {
      await _writeExecutable(fakeFlutter, '''#!/usr/bin/env bash
echo 'Running Xcode build...'
echo 'Xcode build done. 1.0s'
echo 'MOBILE_SMOKE_BODY_STARTED'
sleep 30
''');

      final result = await _runAttempt(
        sandbox,
        fakeFlutter,
        fakeXcrun,
        extraEnvironment: {
          'IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS': '20',
          'IOS_SMOKE_TOTAL_TIMEOUT_SECONDS': '2',
        },
      );

      expect(result.exitCode, 71, reason: '${result.stderr}');
      expect(await _classification(sandbox), 'test_body_stall');
    },
  );

  test(
    'rechecks a body marker arriving during pre-stop diagnostic capture',
    () async {
      final fakePs = File('${sandbox.path}/ps');
      final fakeSleep = File('${sandbox.path}/sleep');
      final captureStarted = File('${sandbox.path}/capture-started');
      final markerObserved = File('${sandbox.path}/marker-observed');
      final pollingResumed = File('${sandbox.path}/polling-resumed');
      final flutterLog = File(
        '${sandbox.path}/diagnostics/initial/flutter.log',
      );
      await _writeExecutable(fakeFlutter, '''#!/usr/bin/env bash
set -euo pipefail
echo 'Running Xcode build...'
echo 'Xcode build done. 1.0s'
for ((poll = 0; poll < 200; poll++)); do
  [[ -f '${captureStarted.path}' ]] && break
  sleep 0.05
done
[[ -f '${captureStarted.path}' ]] || exit 2
echo 'MOBILE_SMOKE_BODY_STARTED'
for ((poll = 0; poll < 100; poll++)); do
  [[ -f '${markerObserved.path}' ]] && break
  sleep 0.05
done
[[ -f '${markerObserved.path}' ]] || exit 3
for ((poll = 0; poll < 100; poll++)); do
  [[ -f '${pollingResumed.path}' ]] && break
  sleep 0.05
done
[[ -f '${pollingResumed.path}' ]] || exit 4
echo 'MOBILE_SMOKE_PASS vertices=13'
echo 'All tests passed!'
''');
      await _writeExecutable(fakePs, '''#!/usr/bin/env bash
set -euo pipefail
touch '${captureStarted.path}'
for ((poll = 0; poll < 50; poll++)); do
  if grep -Fq 'MOBILE_SMOKE_BODY_STARTED' '${flutterLog.path}'; then
    touch '${markerObserved.path}'
    echo 'test body started during process capture'
    exit 0
  fi
  sleep 0.05
done
exit 4
''');
      // The runner must resume polling before Flutter may exit. Otherwise a
      // missing post-capture recheck could pass by observing an already-exited
      // runner instead of preserving the newly started test body.
      await _writeExecutable(fakeSleep, '''#!/usr/bin/env bash
set -euo pipefail
if [[ "\${1:-}" == 1 && -f '${markerObserved.path}' ]]; then
  touch '${pollingResumed.path}'
fi
exec /bin/sleep "\$@"
''');

      final result = await _runAttempt(
        sandbox,
        fakeFlutter,
        fakeXcrun,
        extraEnvironment: {
          'IOS_SMOKE_PS_BIN': fakePs.path,
          'IOS_SMOKE_DIAGNOSTIC_TIMEOUT_SECONDS': '3',
          'IOS_SMOKE_TOTAL_TIMEOUT_SECONDS': '8',
          'PATH': '${sandbox.path}:${Platform.environment['PATH']}',
        },
      );

      expect(result.exitCode, 0, reason: '${result.stderr}');
      expect(await _classification(sandbox), 'success');
      expect(captureStarted.existsSync(), isTrue);
      expect(markerObserved.existsSync(), isTrue);
      expect(pollingResumed.existsSync(), isTrue);
      expect(
        await File(
          '${sandbox.path}/diagnostics/initial/diagnostics/process-tree-before-stop.txt',
        ).readAsString(),
        contains('test body started during process capture'),
      );
    },
  );
}

String get _scriptPath => '${Directory.current.path}/tool/ios_smoke_ci.sh';

Future<void> _writeExecutable(File file, String contents) async {
  await file.writeAsString(contents);
  final chmod = await Process.run('chmod', ['+x', file.path]);
  if (chmod.exitCode != 0) {
    throw ProcessException(
      'chmod',
      ['+x', file.path],
      '${chmod.stderr}',
      chmod.exitCode,
    );
  }
}

Future<ProcessResult> _runAttempt(
  Directory sandbox,
  File flutter,
  File xcrun, {
  Map<String, String> extraEnvironment = const {},
}) => Process.run(
  'bash',
  [
    _scriptPath,
    'run-attempt',
    'SOURCE-DEVICE',
    'initial',
    '${sandbox.path}/diagnostics',
  ],
  workingDirectory: Directory.current.path,
  environment: {
    ...Platform.environment,
    'IOS_SMOKE_FLUTTER_BIN': flutter.path,
    'IOS_SMOKE_XCRUN_BIN': xcrun.path,
    'IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS': '1',
    'IOS_SMOKE_TOTAL_TIMEOUT_SECONDS': '12',
    'IOS_SMOKE_POLL_INTERVAL_SECONDS': '1',
    'IOS_SMOKE_DIAGNOSTIC_TIMEOUT_SECONDS': '1',
    ...extraEnvironment,
  },
);

Future<String> _classification(Directory sandbox) async => (await File(
  '${sandbox.path}/diagnostics/initial/classification.txt',
).readAsString()).trim();

Future<String> _diagnosticText(Directory sandbox) async {
  final output = StringBuffer();
  for (final file in await _diagnosticFiles(sandbox)) {
    output.writeln(await file.readAsString(encoding: utf8));
  }
  return output.toString();
}

Future<List<File>> _diagnosticFiles(Directory sandbox) async {
  final files = <File>[];
  final directory = Directory('${sandbox.path}/diagnostics/initial');
  await for (final entity in directory.list(recursive: true)) {
    if (entity is File &&
        (entity.path.endsWith('.txt') || entity.path.endsWith('.log'))) {
      files.add(entity);
    }
  }
  return files;
}
