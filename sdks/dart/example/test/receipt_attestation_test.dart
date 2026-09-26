import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';

import '../integration_test/support/receipt_attestation.dart';
import '../integration_test/support/receipt_restart_journal.dart';

const _commit = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
const _runId = 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb';
const _target = 'integration_test/example_receipt_test.dart';
final _identityDigest = sha256
    .convert(utf8.encode('four receipt identities'))
    .toString();

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

  ReceiptAttestation restartAttestation(int Function() processId) =>
      ReceiptAttestation(
        testedCommit: _commit,
        target: _target,
        runId: _runId,
        requiredScenarios: const {'receipt_prepared', 'receipt_recovered'},
        requiredRestartCleanups: const {'fixtures', 'journal'},
        output: marker,
        processId: processId,
      );

  Future<Map<String, dynamic>> savedMarker() async =>
      jsonDecode(await marker.readAsString()) as Map<String, dynamic>;

  test('private identity digest is canonical and binds every receipt', () {
    const mutations = ['vertexPut', 'vertexDelete', 'edgeDelete', 'edgeAdd'];
    final identities = <ReceiptHandoffIdentity>[
      for (var index = 0; index < 4; index++)
        (
          logicalOperationId: 'logical-$index',
          recordId: 'record-$index',
          mutation: mutations[index],
          receiptOperationId: List<int>.filled(49, index + 1),
          receiptGroupId: List<int>.filled(16, index + 11),
        ),
    ];
    final digest = receiptIdentitySha256(identities);
    expect(digest, matches(RegExp(r'^[0-9a-f]{64}$')));
    expect(receiptIdentitySha256(identities.reversed), digest);

    final changed = List<ReceiptHandoffIdentity>.of(identities);
    changed[0] = (
      logicalOperationId: identities[0].logicalOperationId,
      recordId: identities[0].recordId,
      mutation: identities[0].mutation,
      receiptOperationId: identities[0].receiptOperationId,
      receiptGroupId: identities[1].receiptGroupId,
    );
    changed[1] = (
      logicalOperationId: identities[1].logicalOperationId,
      recordId: identities[1].recordId,
      mutation: identities[1].mutation,
      receiptOperationId: identities[1].receiptOperationId,
      receiptGroupId: identities[0].receiptGroupId,
    );
    expect(receiptIdentitySha256(changed), isNot(digest));
    final changedOperation = List<ReceiptHandoffIdentity>.of(identities);
    changedOperation[0] = (
      logicalOperationId: identities[0].logicalOperationId,
      recordId: identities[0].recordId,
      mutation: identities[0].mutation,
      receiptOperationId: identities[1].receiptOperationId,
      receiptGroupId: identities[0].receiptGroupId,
    );
    changedOperation[1] = (
      logicalOperationId: identities[1].logicalOperationId,
      recordId: identities[1].recordId,
      mutation: identities[1].mutation,
      receiptOperationId: identities[0].receiptOperationId,
      receiptGroupId: identities[1].receiptGroupId,
    );
    expect(receiptIdentitySha256(changedOperation), isNot(digest));
    expect(() => receiptIdentitySha256(identities.take(3)), throwsStateError);
    expect(
      () => receiptIdentitySha256([
        identities[0],
        identities[0],
        identities[2],
        identities[3],
      ]),
      throwsStateError,
    );
  });

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

  test(
    'only a second process can finish a prepared run after cleanup',
    () async {
      final journal = File('${sandbox.path}/handoff.json');
      var processId = 100;
      final cleaned = <String>[];
      await restartAttestation(() => processId).prepareForRestart(journal, (
        run,
      ) async {
        await run.verifyScenario('receipt_prepared', () async {
          expect(await journal.exists(), isFalse);
        });
        return _identityDigest;
      });
      final prepared = await savedMarker();
      expect(prepared['status'], 'running');
      expect(prepared['phase'], 'awaiting_sigkill');
      expect(prepared['completedScenarios'], ['receipt_prepared']);
      expect(prepared.containsKey('receiptIdentitySha256'), isFalse);
      expect(await journal.exists(), isTrue);
      expect(
        (jsonDecode(await journal.readAsString())
            as Map<String, dynamic>)['receiptIdentitySha256'],
        _identityDigest,
      );

      processId = 101;
      await restartAttestation(() => processId).resumeAfterRestart(journal, (
        run,
      ) async {
        run.registerCleanup(() async {
          cleaned.add('journal');
          await journal.delete();
        }, restartObligation: 'journal');
        run.registerCleanup(
          () => cleaned.add('fixtures'),
          restartObligation: 'fixtures',
        );
        await run.verifyScenario('receipt_recovered', () async {
          expect(await journal.exists(), isTrue);
          expect(run.restartReceiptIdentitySha256, _identityDigest);
        });
      });

      final saved = await savedMarker();
      expect(cleaned, ['fixtures', 'journal']);
      expect(await journal.exists(), isFalse);
      expect(nativeLookups, 3);
      expect(saved['schema'], 2);
      expect(saved['status'], 'passed');
      expect(saved['phase'], 'complete');
      expect(saved.containsKey('receiptIdentitySha256'), isFalse);
      expect(saved['completedScenarios'], [
        'receipt_prepared',
        'receipt_recovered',
      ]);
      final restart = saved['restart'] as Map<String, dynamic>;
      expect(restart['processChanged'], isTrue);
      expect(
        DateTime.parse(
          restart['preparedAt'] as String,
        ).isBefore(DateTime.parse(restart['resumedAt'] as String)),
        isTrue,
      );
    },
  );

  test('a same-process resume cannot claim a SIGKILL', () async {
    final journal = File('${sandbox.path}/handoff.json');
    await restartAttestation(() => 100).prepareForRestart(journal, (run) async {
      await run.verifyScenario('receipt_prepared', () async {});
      return _identityDigest;
    });
    await expectLater(
      restartAttestation(() => 100).resumeAfterRestart(journal, (run) async {}),
      throwsA(isA<StateError>()),
    );
    expect(await marker.exists(), isFalse);
  });

  test('preparation rejects an invalid private identity digest', () async {
    final journal = File('${sandbox.path}/handoff.json');
    await expectLater(
      restartAttestation(() => 100).prepareForRestart(journal, (run) async {
        await run.verifyScenario('receipt_prepared', () async {});
        return 'invalid';
      }),
      throwsA(isA<StateError>()),
    );
    expect(await journal.exists(), isFalse);
    expect((await savedMarker())['status'], 'failed');
  });

  test('missing or malformed handoff cannot reuse a running marker', () async {
    final journal = File('${sandbox.path}/handoff.json');
    await restartAttestation(() => 100).prepareForRestart(journal, (run) async {
      await run.verifyScenario('receipt_prepared', () async {});
      return _identityDigest;
    });
    final originalMarker = await marker.readAsString();
    final originalJournal = await journal.readAsString();
    await journal.delete();
    await expectLater(
      restartAttestation(() => 101).resumeAfterRestart(journal, (run) async {}),
      throwsA(isA<FileSystemException>()),
    );
    expect(await marker.exists(), isFalse);

    await marker.writeAsString(originalMarker);
    await journal.writeAsString(
      originalJournal.replaceFirst('"schema":3', '"schema":3,"schema":3'),
    );
    await expectLater(
      restartAttestation(() => 101).resumeAfterRestart(journal, (run) async {}),
      throwsA(isA<StateError>()),
    );
    expect(await marker.exists(), isFalse);
  });

  test(
    'missing or malformed private identity digest rejects relaunch',
    () async {
      final journal = File('${sandbox.path}/handoff.json');
      await restartAttestation(() => 100).prepareForRestart(journal, (
        run,
      ) async {
        await run.verifyScenario('receipt_prepared', () async {});
        return _identityDigest;
      });
      final preparedMarker = await marker.readAsString();
      final saved =
          jsonDecode(await journal.readAsString()) as Map<String, dynamic>;
      final missing = Map<String, dynamic>.of(saved)
        ..remove('receiptIdentitySha256');
      final malformed = {...saved, 'receiptIdentitySha256': 'invalid'};
      for (final altered in [missing, malformed]) {
        await journal.writeAsString(jsonEncode(altered));
        await expectLater(
          restartAttestation(
            () => 101,
          ).resumeAfterRestart(journal, (run) async {}),
          throwsA(isA<StateError>()),
        );
        expect(await marker.exists(), isFalse);
        await marker.writeAsString(preparedMarker);
      }
    },
  );

  test(
    'changed private receipt identity fails post-relaunch assertions',
    () async {
      final journal = File('${sandbox.path}/handoff.json');
      await restartAttestation(() => 100).prepareForRestart(journal, (
        run,
      ) async {
        await run.verifyScenario('receipt_prepared', () async {});
        return _identityDigest;
      });
      final saved =
          jsonDecode(await journal.readAsString()) as Map<String, dynamic>;
      saved['receiptIdentitySha256'] = sha256
          .convert(utf8.encode('changed receipt association'))
          .toString();
      await journal.writeAsString(jsonEncode(saved));
      await expectLater(
        restartAttestation(() => 101).resumeAfterRestart(journal, (run) async {
          run.registerCleanup(() async {}, restartObligation: 'journal');
          run.registerCleanup(() async {}, restartObligation: 'fixtures');
          await run.verifyScenario('receipt_recovered', () async {
            expect(run.restartReceiptIdentitySha256, _identityDigest);
          });
        }),
        throwsA(isA<TestFailure>()),
      );
      expect((await savedMarker())['status'], 'failed');
    },
  );

  test('foreign run and changed installed bytes reject continuation', () async {
    final journal = File('${sandbox.path}/handoff.json');
    await restartAttestation(() => 100).prepareForRestart(journal, (run) async {
      await run.verifyScenario('receipt_prepared', () async {});
      return _identityDigest;
    });
    final firstMarker = await marker.readAsString();
    await expectLater(
      ReceiptAttestation(
        testedCommit: _commit,
        target: _target,
        runId: List.filled(32, 'c').join(),
        requiredScenarios: const {'receipt_prepared', 'receipt_recovered'},
        requiredRestartCleanups: const {'fixtures', 'journal'},
        output: marker,
        processId: () => 101,
      ).resumeAfterRestart(journal, (run) async {}),
      throwsA(isA<StateError>()),
    );
    await marker.writeAsString(firstMarker);
    await installedApk.writeAsString('another signed build');
    await expectLater(
      restartAttestation(() => 101).resumeAfterRestart(journal, (run) async {}),
      throwsA(isA<StateError>()),
    );
    expect(await marker.exists(), isFalse);
  });

  test(
    'relaunch rejects marker or journal identity and scenario drift',
    () async {
      final journal = File('${sandbox.path}/handoff.json');
      for (final change in <void Function(Map<String, dynamic>)>[
        (value) => value['testedCommit'] = List.filled(40, 'c').join(),
        (value) => value['target'] = 'integration_test/foreign_test.dart',
        (value) => value['platform'] = 'ios',
        (value) => value['packageId'] = 'com.anaregdesign.lanternExample',
        (value) => value['completedScenarios'] = ['receipt_recovered'],
        (value) => value['installedBinarySha256'] = List.filled(64, 'f').join(),
      ]) {
        if (await journal.exists()) await journal.delete();
        await restartAttestation(() => 100).prepareForRestart(journal, (
          run,
        ) async {
          await run.verifyScenario('receipt_prepared', () async {});
          return _identityDigest;
        });
        final saved = await savedMarker();
        change(saved);
        await marker.writeAsString(jsonEncode(saved));
        await expectLater(
          restartAttestation(
            () => 101,
          ).resumeAfterRestart(journal, (run) async {}),
          throwsA(isA<StateError>()),
        );
        expect(await marker.exists(), isFalse);
      }
      await journal.delete();
      await restartAttestation(() => 100).prepareForRestart(journal, (
        run,
      ) async {
        await run.verifyScenario('receipt_prepared', () async {});
        return _identityDigest;
      });
      final saved =
          jsonDecode(await journal.readAsString()) as Map<String, dynamic>;
      saved['requiredScenarios'] = ['receipt_prepared', 'receipt_spoofed'];
      await journal.writeAsString(jsonEncode(saved));
      await expectLater(
        restartAttestation(
          () => 101,
        ).resumeAfterRestart(journal, (run) async {}),
        throwsA(isA<StateError>()),
      );
      expect(await marker.exists(), isFalse);
    },
  );

  test('missing or failed restart cleanup never produces a pass', () async {
    for (final failCleanup in [false, true]) {
      final journal = File('${sandbox.path}/handoff.json');
      if (await journal.exists()) await journal.delete();
      await restartAttestation(() => 100).prepareForRestart(journal, (
        run,
      ) async {
        await run.verifyScenario('receipt_prepared', () async {});
        return _identityDigest;
      });
      await expectLater(
        restartAttestation(() => 101).resumeAfterRestart(journal, (run) async {
          run.registerCleanup(() async {}, restartObligation: 'journal');
          if (failCleanup) {
            run.registerCleanup(
              () => throw StateError('fixture cleanup failed'),
              restartObligation: 'fixtures',
            );
          }
          await run.verifyScenario('receipt_recovered', () async {});
        }),
        throwsA(isA<StateError>()),
      );
      final saved = await savedMarker();
      expect(saved['status'], 'failed');
      expect(saved['failureType'], failCleanup ? 'cleanup' : 'incomplete');
    }
  });

  test('a restart missing post-relaunch assertions cannot pass', () async {
    final journal = File('${sandbox.path}/handoff.json');
    await restartAttestation(() => 100).prepareForRestart(journal, (run) async {
      await run.verifyScenario('receipt_prepared', () async {});
      return _identityDigest;
    });
    await expectLater(
      restartAttestation(() => 101).resumeAfterRestart(journal, (run) async {
        run.registerCleanup(() async {}, restartObligation: 'journal');
        run.registerCleanup(() async {}, restartObligation: 'fixtures');
      }),
      throwsA(isA<StateError>()),
    );
    final saved = await savedMarker();
    expect(saved['schema'], 2);
    expect(saved['status'], 'failed');
    expect(saved['failureType'], 'incomplete');
    expect(saved['completedScenarios'], ['receipt_prepared']);
  });

  test('the prepare phase never passes without a restart', () async {
    final journal = File('${sandbox.path}/handoff.json');
    await expectLater(
      restartAttestation(() => 100).prepareForRestart(journal, (run) async {
        await run.verifyScenario('receipt_prepared', () async {});
        await run.verifyScenario('receipt_recovered', () async {});
        return _identityDigest;
      }),
      throwsA(isA<StateError>()),
    );
    expect((await savedMarker())['status'], 'failed');
    expect(await journal.exists(), isFalse);
  });
}
