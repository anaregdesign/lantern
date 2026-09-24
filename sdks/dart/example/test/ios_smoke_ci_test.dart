import 'dart:async';
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

  test('reports success after terminal pass markers', () async {
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

  test(
    'attached iOS smoke uses the driver result and redacts its VM URL',
    () async {
      await _writeAttachedFakes(sandbox, fakeFlutter, fakeXcrun);

      final result = await _runAttachedAttempt(sandbox, fakeFlutter, fakeXcrun);

      expect(result.exitCode, 0, reason: '${result.stderr}');
      expect(await _classification(sandbox), 'success');
      expect(result.stdout.toString(), contains('All tests passed.'));
      expect(result.stdout.toString(), isNot(contains('private-vm-code')));
      final logs = await _diagnosticText(sandbox);
      expect(logs, contains('MOBILE_SMOKE_PASS'));
      expect(logs, contains('<redacted-url>'));
      expect(logs, isNot(contains('private-vm-code')));
      await _expectPhases(sandbox, _allPhases);
    },
  );

  test('attached iOS smoke preserves an assertion failure', () async {
    await _writeAttachedFakes(sandbox, fakeFlutter, fakeXcrun, driverExit: 1);

    final result = await _runAttachedAttempt(sandbox, fakeFlutter, fakeXcrun);

    expect(result.exitCode, isNonZero);
    expect(await _classification(sandbox), 'test_failure');
    expect(
      await File(
        '${sandbox.path}/diagnostics/initial/driver.log',
      ).readAsString(),
      contains('Expected: true Actual: false'),
    );
  });

  test('attached iOS smoke bounds a closed-pipe live launch', () async {
    await _writeAttachedFakes(
      sandbox,
      fakeFlutter,
      fakeXcrun,
      closedPipeLaunch: true,
    );
    final stopwatch = Stopwatch()..start();

    final result = await _runAttachedAttempt(sandbox, fakeFlutter, fakeXcrun);

    stopwatch.stop();
    expect(result.exitCode, isNonZero);
    expect(await _classification(sandbox), 'launch_stall');
    expect(stopwatch.elapsed, lessThan(const Duration(seconds: 12)));
  });

  test('attached iOS smoke does not retry an exited Runner', () async {
    await _writeAttachedFakes(
      sandbox,
      fakeFlutter,
      fakeXcrun,
      runnerExited: true,
    );

    final result = await _runAttachedAttempt(sandbox, fakeFlutter, fakeXcrun);

    expect(result.exitCode, isNonZero);
    expect(await _classification(sandbox), 'launch_failure');
  });

  test('attached iOS smoke does not retry an app-side test failure', () async {
    await _writeAttachedFakes(sandbox, fakeFlutter, fakeXcrun, appFailed: true);

    final result = await _runAttachedAttempt(sandbox, fakeFlutter, fakeXcrun);

    expect(result.exitCode, isNonZero);
    expect(await _classification(sandbox), 'test_failure');
  });

  test(
    'attached iOS smoke retries when VM attachment is unavailable',
    () async {
      await _writeAttachedFakes(
        sandbox,
        fakeFlutter,
        fakeXcrun,
        omitVmUrl: true,
      );

      final result = await _runAttachedAttempt(sandbox, fakeFlutter, fakeXcrun);

      expect(result.exitCode, isNonZero);
      expect(await _classification(sandbox), 'attach_stall');
    },
  );

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

  test(
    'bounds noisy hung output while the producer is still running',
    () async {
      await _writeExecutable(fakeFlutter, r'''#!/usr/bin/env python3
import os
import time

os.write(1, b'Running Xcode build...\nXcode build done. 1.0s\n')
chunk = (b'noisy simulator output ' + b'x' * 1000 + b'\n') * 128
while True:
    os.write(1, chunk)
    time.sleep(0.025)
''');

      final observed = await _observeAttempt(sandbox, fakeFlutter, fakeXcrun);

      expect(observed.result.exitCode, isNonZero);
      expect(await _classification(sandbox), 'launch_stall');
      expect(observed.sampleCount, greaterThan(5));
      expect(observed.peakLogBytes, greaterThan(0));
      expect(observed.peakLogBytes, lessThanOrEqualTo(262144));
      expect(
        utf8.encode(observed.result.stdout.toString()).length,
        lessThanOrEqualTo(262144 + 4096),
        reason: 'The console must stop growing after its bounded output budget',
      );
      expect(observed.elapsed, lessThan(const Duration(seconds: 20)));
      await _expectPhases(sandbox, ['build_started', 'build_done']);
    },
  );

  test(
    'drains huge newline-free output and recognizes its phase markers',
    () async {
      await _writeExecutable(fakeFlutter, r'''#!/usr/bin/env python3
import os
import time

for _ in range(32):
    os.write(1, b'x' * 262144)
for marker in (
    b'Running Xcode build...',
    b'Xcode build done. 1.0s',
    b'MOBILE_SMOKE_BODY_STARTED',
    b'MOBILE_SMOKE_PASS vertices=13',
    b'All tests passed!',
):
    split = len(marker) // 2
    os.write(1, marker[:split])
    time.sleep(0.03)
    os.write(1, marker[split:] + b' ')
os.write(1, b'\n')
''');

      final observed = await _observeAttempt(sandbox, fakeFlutter, fakeXcrun);

      expect(observed.result.exitCode, 0, reason: '${observed.result.stderr}');
      expect(await _classification(sandbox), 'success');
      expect(observed.peakLogBytes, lessThanOrEqualTo(262144));
      expect(
        utf8.encode(observed.result.stdout.toString()).length,
        lessThanOrEqualTo(262144 + 4096),
      );
      expect(observed.elapsed, lessThan(const Duration(seconds: 20)));
      await _expectPhases(sandbox, _allPhases);
    },
  );

  for (final classification in ['success', 'test_body_stall', 'launch_stall']) {
    test(
      'retains early phase markers after noisy $classification output',
      () async {
        final bodyStarted = classification != 'launch_stall';
        final success = classification == 'success';
        await _writeExecutable(fakeFlutter, '''#!/usr/bin/env python3
import os
import time

os.write(1, b'Running Xcode build...\\nXcode build done. 1.0s\\n')
${bodyStarted ? "os.write(1, b'MOBILE_SMOKE_BODY_STARTED\\n')" : ''}
${success ? "os.write(1, b'MOBILE_SMOKE_PASS vertices=13\\nAll tests passed!\\n')" : ''}
for _ in range(768):
    os.write(1, b'x' * 1023 + b'\\n')
os.write(1, b'diagnostic-tail-after-noise\\n')
${success ? '' : 'time.sleep(30)'}
''');

        final result = await _runAttempt(
          sandbox,
          fakeFlutter,
          fakeXcrun,
          extraEnvironment: {'IOS_SMOKE_TOTAL_TIMEOUT_SECONDS': '3'},
        );

        expect(
          result.exitCode,
          success ? 0 : isNonZero,
          reason: '${result.stderr}',
        );
        expect(await _classification(sandbox), classification);
        await _expectPhases(sandbox, [
          'build_started',
          'build_done',
          if (bodyStarted) 'body_started',
          if (success) ...['smoke_pass', 'tests_passed'],
        ]);
        final log = File('${sandbox.path}/diagnostics/initial/flutter.log');
        expect(await log.length(), lessThanOrEqualTo(262144));
        expect(
          await log.readAsString(),
          contains('diagnostic-tail-after-noise'),
        );
      },
    );
  }

  test('redacts split secrets and bounds URL replacement expansion', () async {
    await _writeExecutable(fakeFlutter, r'''#!/usr/bin/env python3
import os
import time

os.write(1, b'Running Xcode build...\nXcode build done. 1.0s\n')
os.write(1, b'MOBILE_SMOKE_BODY_STARTED\n')
parts = (
    b'Authorization: Bea',
    b'rer split-bearer-secret http',
    b's://split.private.test/path mobile-',
    b'smoke:split-mobile-secret TOKEN=split-',
    b'token-secret\n',
)
for part in parts:
    os.write(1, part)
    time.sleep(0.03)
for _ in range(30000):
    os.write(1, b'http://x\n')
for part in parts:
    os.write(1, part)
    time.sleep(0.03)
os.write(1, b'MOBILE_SMOKE_PASS vertices=13\nAll tests passed!\n')
''');

    final result = await _runAttempt(sandbox, fakeFlutter, fakeXcrun);

    expect(result.exitCode, 0, reason: '${result.stderr}');
    expect(await _classification(sandbox), 'success');
    final log = File('${sandbox.path}/diagnostics/initial/flutter.log');
    expect(await log.length(), lessThanOrEqualTo(262144));
    expect(
      utf8.encode(result.stdout.toString()).length,
      lessThanOrEqualTo(262144 + 4096),
    );
    for (final text in [result.stdout.toString(), await log.readAsString()]) {
      for (final secret in [
        'split-bearer-secret',
        'split.private.test',
        'split-mobile-secret',
        'split-token-secret',
        'http://x',
      ]) {
        expect(text, isNot(contains(secret)), reason: 'Leaked $secret');
      }
      expect(text, contains('<redacted>'));
      expect(text, contains('<redacted-url>'));
    }
    await _expectPhases(sandbox, _allPhases);
  });

  test('clears stale phase flags before reusing an attempt label', () async {
    final phases = Directory('${sandbox.path}/diagnostics/initial/phases');
    await phases.create(recursive: true);
    for (final phase in _allPhases) {
      await File('${phases.path}/$phase').writeAsString('seen\n');
    }
    await _writeExecutable(fakeFlutter, '''#!/usr/bin/env bash
echo 'failed before launch'
exit 1
''');

    final result = await _runAttempt(sandbox, fakeFlutter, fakeXcrun);

    expect(result.exitCode, isNonZero);
    expect(await _classification(sandbox), 'pre_body_failure');
    for (final phase in _allPhases) {
      expect(
        File('${phases.path}/$phase').existsSync(),
        isFalse,
        reason: phase,
      );
    }
  });

  test(
    'finalizes both noisy attempts within the aggregate artifact bounds',
    () async {
      final diagnostics = Directory('${sandbox.path}/diagnostics');
      final classifications = {'initial': 'launch_stall', 'retry': 'success'};
      for (final entry in classifications.entries) {
        final attempt = Directory('${diagnostics.path}/${entry.key}');
        await Directory('${attempt.path}/phases').create(recursive: true);
        await File(
          '${attempt.path}/classification.txt',
        ).writeAsString('${entry.value}\n');
        for (final phase in _allPhases) {
          await File('${attempt.path}/phases/$phase').writeAsString('true\n');
        }
        await Directory('${attempt.path}/diagnostics').create();
        for (var index = 0; index < 10; index++) {
          final name = index == 0
              ? 'flutter.log'
              : 'diagnostics/capture-$index.log';
          await File('${attempt.path}/$name').writeAsString(
            '${'http://x\n' * 32768}'
            'tail-${entry.key}-$index Authorization: Bearer aggregate-secret\n',
          );
        }
      }
      await File('${diagnostics.path}/partial.raw').writeAsString('raw-secret');

      final result = await Process.run('bash', [
        _scriptPath,
        'finalize-diagnostics',
        diagnostics.path,
      ], workingDirectory: Directory.current.path);

      expect(result.exitCode, 0, reason: '${result.stderr}');
      final files = await diagnostics
          .list(recursive: true)
          .where((entry) => entry is File)
          .cast<File>()
          .toList();
      expect(files.length, lessThanOrEqualTo(32));
      var totalBytes = 0;
      for (final file in files) {
        final size = await file.length();
        totalBytes += size;
        expect(size, lessThanOrEqualTo(262144), reason: file.path);
        final text = await file.readAsString();
        expect(text, isNot(contains('aggregate-secret')), reason: file.path);
        expect(text, isNot(contains('http://x')), reason: file.path);
        if (file.path.endsWith('.log')) {
          expect(
            text,
            contains('tail-'),
            reason: 'Retained logs keep useful tails',
          );
        }
      }
      expect(totalBytes, lessThanOrEqualTo(2097152));
      for (final entry in classifications.entries) {
        final attempt = '${diagnostics.path}/${entry.key}';
        expect(
          await File('$attempt/classification.txt').readAsString(),
          '${entry.value}\n',
        );
        expect(
          await File('$attempt/flutter.log').readAsString(),
          contains('tail-${entry.key}-0'),
        );
        for (final phase in _allPhases) {
          expect(
            File('$attempt/phases/$phase').existsSync(),
            isTrue,
            reason: phase,
          );
        }
      }
      expect(File('${diagnostics.path}/partial.raw').existsSync(), isFalse);
      expect(
        await File('${diagnostics.path}/finalized.txt').readAsString(),
        'bounded=true redacted=true\n',
      );
    },
  );
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

const _allPhases = [
  'build_started',
  'build_done',
  'body_started',
  'smoke_pass',
  'tests_passed',
];

Future<void> _expectPhases(Directory sandbox, List<String> phases) async {
  for (final phase in phases) {
    expect(
      await File('${sandbox.path}/diagnostics/initial/phases/$phase').exists(),
      isTrue,
      reason: 'Phase $phase must survive diagnostic truncation',
    );
  }
}

Future<
  ({ProcessResult result, int peakLogBytes, int sampleCount, Duration elapsed})
>
_observeAttempt(Directory sandbox, File flutter, File xcrun) async {
  final stopwatch = Stopwatch()..start();
  final process = await Process.start(
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
      'IOS_SMOKE_TOTAL_TIMEOUT_SECONDS': '4',
      'IOS_SMOKE_POLL_INTERVAL_SECONDS': '1',
      'IOS_SMOKE_DIAGNOSTIC_TIMEOUT_SECONDS': '1',
    },
  );
  final stdout = process.stdout.transform(utf8.decoder).join();
  final stderr = process.stderr.transform(utf8.decoder).join();
  final log = File('${sandbox.path}/diagnostics/initial/flutter.log');
  var peakLogBytes = 0;
  var sampleCount = 0;
  void sampleLog() {
    try {
      if (!log.existsSync()) return;
      final size = log.lengthSync();
      if (size > peakLogBytes) peakLogBytes = size;
      sampleCount++;
    } on FileSystemException {
      // An atomic finalization can replace the file between existence and stat.
    }
  }

  final timer = Timer.periodic(
    const Duration(milliseconds: 10),
    (_) => sampleLog(),
  );
  try {
    final exitCode = await process.exitCode.timeout(
      const Duration(seconds: 25),
    );
    sampleLog();
    final result = ProcessResult(
      process.pid,
      exitCode,
      await stdout,
      await stderr,
    );
    stopwatch.stop();
    return (
      result: result,
      peakLogBytes: peakLogBytes,
      sampleCount: sampleCount,
      elapsed: stopwatch.elapsed,
    );
  } finally {
    timer.cancel();
    process.kill(ProcessSignal.sigkill);
  }
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

Future<ProcessResult> _runAttachedAttempt(
  Directory sandbox,
  File flutter,
  File xcrun,
) => Process.run(
  'bash',
  [
    _scriptPath,
    'run-attached-attempt',
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
    'IOS_SMOKE_TOTAL_TIMEOUT_SECONDS': '4',
    'IOS_SMOKE_BUILD_TIMEOUT_SECONDS': '2',
    'IOS_SMOKE_ATTACH_TIMEOUT_SECONDS': '2',
    'IOS_SMOKE_DIAGNOSTIC_TIMEOUT_SECONDS': '1',
  },
);

Future<void> _writeAttachedFakes(
  Directory sandbox,
  File flutter,
  File xcrun, {
  int driverExit = 0,
  bool closedPipeLaunch = false,
  bool appFailed = false,
  bool omitVmUrl = false,
  bool runnerExited = false,
}) async {
  final appLog = File('${sandbox.path}/app-log.json');
  await appLog.writeAsString(
    jsonEncode([
      if (!omitVmUrl)
        {
          'eventMessage':
              'flutter: The Dart VM service is listening on '
              'http://127.0.0.1:1234/private-vm-code/',
        },
      if (!runnerExited) ...[
        {'eventMessage': 'flutter: MOBILE_SMOKE_BODY_STARTED'},
        {'eventMessage': 'flutter: MOBILE_SMOKE_PASS vertices=13'},
        {
          'eventMessage': appFailed
              ? 'flutter: 00:00 +0 -1: Some tests failed.'
              : 'flutter: 00:00 +1: All tests passed!',
        },
      ],
    ]),
  );
  await _writeExecutable(
    flutter,
    r'''#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  build)
    [[ " $* " == *'--dart-define=LANTERN_IOS_DIRECT_LAUNCH=true'* ]]
    echo 'Running Xcode build...'
    echo 'Xcode build done. 1.0s'
    ;;
  drive)
    echo 'VMServiceFlutterDriver: Connected to Flutter application.'
    if [[ _DRIVER_EXIT_ == 0 ]]; then
      echo 'All tests passed.'
    else
      echo 'Failure Details:'
      echo 'Expected: true Actual: false'
    fi
    exit _DRIVER_EXIT_
    ;;
  *) exit 2 ;;
esac
'''
        .replaceAll('_DRIVER_EXIT_', '$driverExit'),
  );
  await _writeExecutable(
    xcrun,
    r'''#!/usr/bin/env bash
set -euo pipefail
case "${2:-}" in
  terminate|install) exit 0 ;;
  launch)
    if [[ '_CLOSED_PIPE_LAUNCH_' == true ]]; then
      exec >/dev/null 2>&1
      sleep 30
    fi
    if [[ '_RUNNER_EXITED_' == true ]]; then
      sleep 0.01 &
      exited_pid=$!
      wait "$exited_pid"
      echo "com.anaregdesign.lanternExample: $exited_pid"
    else
      echo "com.anaregdesign.lanternExample: $PPID"
    fi
    ;;
  spawn)
    if [[ "${4:-}" == log ]]; then
      cat '_APP_LOG_'
    fi
    ;;
  *)
    echo 'Authorization: Bearer fake-private-token http://private.example.test/path'
    ;;
esac
'''
        .replaceAll('_CLOSED_PIPE_LAUNCH_', '$closedPipeLaunch')
        .replaceAll('_RUNNER_EXITED_', '$runnerExited')
        .replaceAll('_APP_LOG_', appLog.path),
  );
}

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
