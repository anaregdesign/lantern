import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';

import '../integration_test/support/receipt_attestation.dart';

const _commit = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const _runId = 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb';
const _target = 'integration_test/example_receipt_test.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  late Directory sandbox;
  late File installedApk;
  late File marker;
  late int nativeLookups;
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;

  setUp(() async {
    nativeLookups = 0;
    sandbox = await Directory.systemTemp.createTemp('receipt-attestation-');
    installedApk = File('${sandbox.path}/base.apk');
    await installedApk.writeAsString('target-specific APK bytes');
    marker = File('${sandbox.path}/$receiptAttestationFileName');
    messenger.setMockMethodCallHandler(installedReceiptBinaryChannel, (
      call,
    ) async {
      nativeLookups++;
      expect(call.method, 'installedBinary');
      return {
        'platform': 'android',
        'packageId': 'com.anaregdesign.lantern_example',
        'path': installedApk.path,
      };
    });
  });

  tearDown(() async {
    messenger.setMockMethodCallHandler(installedReceiptBinaryChannel, null);
    await sandbox.delete(recursive: true);
  });

  ReceiptAttestation attestation({
    Set<String> requiredScenarios = const {'receipt_applied'},
  }) => ReceiptAttestation(
    testedCommit: _commit,
    target: _target,
    runId: _runId,
    requiredScenarios: requiredScenarios,
    output: marker,
  );

  Future<Map<String, dynamic>> savedMarker() async =>
      jsonDecode(await marker.readAsString()) as Map<String, dynamic>;

  test('hashes installed target bytes and passes only after cleanup', () async {
    final cleanupObserved = <String>[];
    await attestation().run((run) async {
      run.registerCleanup(() async {
        final duringCleanup = await savedMarker();
        expect(duringCleanup['status'], 'running');
        expect(duringCleanup['completedScenarios'], ['receipt_applied']);
        cleanupObserved.add('done');
      });
      await run.verifyScenario('receipt_applied', () async {
        expect(1 + 1, 2);
      });
    });

    final saved = await savedMarker();
    expect(cleanupObserved, ['done']);
    expect(nativeLookups, 2);
    expect(saved['status'], 'passed');
    expect(saved['phase'], 'complete');
    expect(saved['completedScenarios'], ['receipt_applied']);
    expect(saved['testedCommit'], _commit);
    expect(saved['target'], _target);
    expect(saved['runId'], _runId);
    expect(saved['platform'], 'android');
    expect(
      saved['installedBinarySha256'],
      sha256.convert(utf8.encode('target-specific APK bytes')).toString(),
    );
    expect(DateTime.parse(saved['startedAt'] as String).isUtc, isTrue);
    expect(DateTime.parse(saved['finishedAt'] as String).isUtc, isTrue);
  });

  for (final mutationPhase in ['body', 'cleanup']) {
    test(
      'changed installed bytes during $mutationPhase fail attestation',
      () async {
        await expectLater(
          attestation().run((run) async {
            run.registerCleanup(() async {
              if (mutationPhase == 'cleanup') {
                await installedApk.writeAsString('changed during cleanup');
              }
            });
            await run.verifyScenario('receipt_applied', () async {
              expect(1, 1);
            });
            if (mutationPhase == 'body') {
              await installedApk.writeAsString('changed during body');
            }
          }),
          throwsA(isA<StateError>()),
        );

        final saved = await savedMarker();
        expect(nativeLookups, 2);
        expect(saved['status'], 'failed');
        expect(saved['phase'], 'attestation');
        expect(saved['failureType'], 'attestation');
        expect(saved['completedScenarios'], ['receipt_applied']);
        expect(
          saved['installedBinarySha256'],
          sha256.convert(utf8.encode('target-specific APK bytes')).toString(),
        );
      },
    );
  }

  test('unreadable installed binary after cleanup fails attestation', () async {
    await expectLater(
      attestation().run((run) async {
        run.registerCleanup(() async {
          await installedApk.delete();
        });
        await run.verifyScenario('receipt_applied', () async {
          expect(1, 1);
        });
      }),
      throwsA(isA<FileSystemException>()),
    );
    final saved = await savedMarker();
    expect(nativeLookups, 2);
    expect(saved['status'], 'failed');
    expect(saved['phase'], 'attestation');
    expect(saved['failureType'], 'attestation');
  });

  test(
    'changed installed platform and package fail with matching bytes',
    () async {
      final iosApp = File('${sandbox.path}/Frameworks/App.framework/App');
      await iosApp.parent.create(recursive: true);
      await iosApp.writeAsString('target-specific APK bytes');
      var switchIdentity = false;
      messenger.setMockMethodCallHandler(installedReceiptBinaryChannel, (
        call,
      ) async {
        expect(call.method, 'installedBinary');
        if (switchIdentity) {
          return {
            'platform': 'ios',
            'packageId': 'com.anaregdesign.lanternExample',
            'path': iosApp.path,
          };
        }
        return {
          'platform': 'android',
          'packageId': 'com.anaregdesign.lantern_example',
          'path': installedApk.path,
        };
      });

      await expectLater(
        attestation().run((run) async {
          await run.verifyScenario('receipt_applied', () async {
            expect(1, 1);
          });
          switchIdentity = true;
        }),
        throwsA(isA<StateError>()),
      );
      final saved = await savedMarker();
      expect(saved['status'], 'failed');
      expect(saved['phase'], 'attestation');
      expect(saved['failureType'], 'attestation');
    },
  );

  test('assertion failure cannot mark a scenario as completed', () async {
    var cleaned = false;
    await expectLater(
      attestation().run((run) async {
        run.registerCleanup(() {
          cleaned = true;
        });
        await run.verifyScenario('receipt_applied', () async {
          expect(1, 2);
        });
      }),
      throwsA(isA<TestFailure>()),
    );
    final saved = await savedMarker();
    expect(cleaned, isTrue);
    expect(saved['status'], 'failed');
    expect(saved['failureType'], 'body');
    expect(saved['completedScenarios'], isEmpty);
  });

  test('failed cleanup cannot produce a pass', () async {
    await expectLater(
      attestation().run((run) async {
        run.registerCleanup(() => throw StateError('cleanup failed'));
        await run.verifyScenario('receipt_applied', () async {
          expect(1, 1);
        });
      }),
      throwsA(isA<StateError>()),
    );
    final saved = await savedMarker();
    expect(saved['status'], 'failed');
    expect(saved['phase'], 'cleanup');
    expect(saved['failureType'], 'cleanup');
  });

  test('both body and cleanup failures remain visible', () async {
    await expectLater(
      attestation().run((run) async {
        run.registerCleanup(() => throw StateError('cleanup failed'));
        throw StateError('body failed');
      }),
      throwsA(
        isA<ReceiptAttestationFailure>()
            .having((error) => error.phases, 'phases', ['body', 'cleanup'])
            .having((error) => error.errors.length, 'error count', 2),
      ),
    );
    expect((await savedMarker())['failureType'], 'cleanup');
  });

  test('missing or duplicate scenarios fail closed', () async {
    await expectLater(
      attestation().run((run) async {}),
      throwsA(isA<StateError>()),
    );
    expect((await savedMarker())['failureType'], 'incomplete');
    await expectLater(
      attestation().run((run) async {
        await run.verifyScenario('receipt_applied', () async {
          expect(1, 1);
        });
        await run.verifyScenario('receipt_applied', () async {
          expect(2, 2);
        });
      }),
      throwsA(isA<StateError>()),
    );
    expect((await savedMarker())['status'], 'failed');
  });

  test('no stale pass remains if native installed lookup fails', () async {
    await marker.writeAsString('{"status":"passed"}');
    messenger.setMockMethodCallHandler(
      installedReceiptBinaryChannel,
      (call) async => {
        'platform': 'android',
        'packageId': 'com.anaregdesign.lantern_example',
        'path': '${sandbox.path}/missing.apk',
      },
    );
    await expectLater(
      attestation().run((run) async {}),
      throwsA(isA<FileSystemException>()),
    );
    expect(await marker.exists(), isFalse);
  });

  test('iOS hashes App.framework/App, not the Runner launcher', () async {
    final app = File('${sandbox.path}/Frameworks/App.framework/App');
    await app.parent.create(recursive: true);
    await app.writeAsString('installed Dart AOT');
    messenger.setMockMethodCallHandler(
      installedReceiptBinaryChannel,
      (call) async => {
        'platform': 'ios',
        'packageId': 'com.anaregdesign.lanternExample',
        'path': app.path,
      },
    );
    final binary = await readInstalledReceiptBinary();
    expect(binary.platform, 'ios');
    expect(
      binary.sha256,
      sha256.convert(utf8.encode('installed Dart AOT')).toString(),
    );
    messenger.setMockMethodCallHandler(
      installedReceiptBinaryChannel,
      (call) async => {
        'platform': 'ios',
        'packageId': 'com.anaregdesign.lanternExample',
        'path': '${sandbox.path}/Runner',
      },
    );
    await expectLater(readInstalledReceiptBinary(), throwsA(isA<StateError>()));
  });
}
