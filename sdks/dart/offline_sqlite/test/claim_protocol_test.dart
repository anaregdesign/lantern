import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';

import '../tool/claim_probe.dart' show checkedClaimWorkerPid;
import '../tool/claim_protocol.dart';
import '../tool/probe_protocol.dart';

void main() {
  group('fixed claim failures', () {
    test('invalid PIDs never become cleanup signal targets', () {
      for (final value in [0, -1, -12345]) {
        int? target;
        expect(
          () => target = checkedClaimWorkerPid(value),
          throwsA(
            isA<ClaimProblem>().having(
              (failure) => failure.reason,
              'reason',
              ClaimReason.invalidPid,
            ),
          ),
        );
        expect(target, isNull);
      }
      for (final value in [1, 12345]) {
        expect(checkedClaimWorkerPid(value), value);
      }
    });
    test('crash adapter accepts only a typed claim failure', () {
      expect(
        claimFailureCode(
          ClaimProbeFailure(
            ClaimStage.competingClaims,
            ClaimReason.singleWinner,
          ),
        ),
        'claim:competingClaims:singleWinner',
      );
      expect(
        claimFailureCode(StateError('claim:competingClaims:singleWinner')),
        isNull,
      );
      expect(claimFailureCode(Exception('private credential')), isNull);
    });
    test('stage and distinct contract survive cleanup failures', () async {
      final calls = <int>[];
      ClaimProbeFailure? retained;
      try {
        await withClaimCleanup<void>(
          stage: () => ClaimStage.renewCas,
          body: () async => throw const ClaimProblem(ClaimReason.ownerRenew),
          cleanups: () => [
            () async {
              calls.add(1);
              throw TimeoutException('private deadline');
            },
            () async {
              calls.add(2);
            },
            () async {
              calls.add(3);
              throw Exception('private filesystem path');
            },
          ],
        );
      } on ClaimProbeFailure catch (failure) {
        retained = failure;
      }
      expect(calls, [1, 2, 3]);
      expect(retained?.code, 'claim:renewCas:ownerRenew');
      expect(retained?.cleanupAttempted, 3);
      expect(retained?.cleanupSucceeded, 1);
      expect(retained?.cleanupFailures, [
        ClaimReason.timeout,
        ClaimReason.unexpected,
      ]);
      expect(jsonEncode(retained?.snapshot()), isNot(contains('private')));
    });

    test(
      'nested cleanup completeness stays separate from the primary',
      () async {
        await expectLater(
          withClaimCleanup<void>(
            stage: () => ClaimStage.seed,
            body: () => withClaimCleanup<void>(
              stage: () => ClaimStage.seed,
              body: () async => throw const ClaimProblem(
                ClaimReason.workerExit,
                workerStage: ClaimWorkerStage.verify,
                workerReason: ClaimReason.outboxRecord,
              ),
              cleanups: () => [
                () async => throw const ClaimProblem(ClaimReason.collector),
              ],
            ),
            cleanups: () => [() async {}],
          ),
          throwsA(
            isA<ClaimProbeFailure>()
                .having(
                  (failure) => failure.reason,
                  'primary',
                  ClaimReason.workerExit,
                )
                .having(
                  (failure) => failure.workerReason,
                  'worker assertion',
                  ClaimReason.outboxRecord,
                )
                .having((failure) => failure.cleanupAttempted, 'attempted', 2)
                .having((failure) => failure.cleanupSucceeded, 'succeeded', 1)
                .having(
                  (failure) => failure.cleanupFailures,
                  'cleanup failures',
                  [ClaimReason.collector],
                ),
          ),
        );
      },
    );

    test(
      'cleanup alone remains fatal and successful cleanup returns result',
      () async {
        await expectLater(
          withClaimCleanup(
            stage: () => ClaimStage.freshProcessVerify,
            body: () async => 7,
            cleanups: () => [() async => throw StateError('private')],
          ),
          throwsA(
            isA<ClaimProbeFailure>()
                .having((failure) => failure.stage, 'stage', ClaimStage.cleanup)
                .having(
                  (failure) => failure.reason,
                  'reason',
                  ClaimReason.cleanup,
                ),
          ),
        );
        expect(
          await withClaimCleanup(
            stage: () => ClaimStage.seed,
            body: () async => 7,
            cleanups: () => [() async {}],
          ),
          7,
        );
      },
    );

    test('worker markers accept only finite stages and reasons', () {
      final marker = claimWorkerFailure(
        ClaimWorkerStage.opening,
        StateError('private SQL'),
      );
      final message = parseClaimMessage(marker);
      expect(message.failure?.workerStage, ClaimWorkerStage.opening);
      expect(message.failure?.workerReason, ClaimReason.contract);
      expect(jsonEncode(marker), isNot(contains('private')));
      for (final bad in [
        {...marker, 'path': 'private path'},
        {...marker, 'stage': 'private stage'},
        {...marker, 'reason': 'private error'},
        {'event': 'claimed', 'count': 'private payload'},
      ]) {
        expect(() => parseClaimMessage(bad), throwsA(isA<ClaimProblem>()));
      }
      expect(
        parseClaimWorkerPhase(claimWorkerPhase(ClaimWorkerStage.recover)),
        ClaimWorkerStage.recover,
      );
      expect(
        parseClaimWorkerPhase({'event': 'claim_phase', 'stage': 'private'}),
        isNull,
      );
      expect(
        parseClaimWorkerPhase({
          ...claimWorkerPhase(ClaimWorkerStage.ready),
          'path': 'private',
        }),
        isNull,
      );
    });
  });

  group('bounded claim messages', () {
    test('required event and typed worker rejection remain distinct', () async {
      final messages = ClaimMessages();
      messages.add(
        utf8.encode('${jsonEncode({'event': 'started', 'pid': 12345})}\n'),
      );
      expect((await messages.read(ClaimEvent.started)).pid, 12345);
      messages.add(
        utf8.encode(
          '${jsonEncode(claimWorkerFailure(ClaimWorkerStage.verify, const ClaimProblem(ClaimReason.operationRecord)))}\n',
        ),
      );
      await expectLater(
        messages.read(ClaimEvent.verified),
        throwsA(
          isA<ClaimProblem>()
              .having(
                (failure) => failure.reason,
                'reason',
                ClaimReason.workerExit,
              )
              .having(
                (failure) => failure.workerReason,
                'worker assertion',
                ClaimReason.operationRecord,
              ),
        ),
      );
      expect(messages.lastExpected, ClaimEvent.verified);
      expect(messages.lastObserved, ClaimEvent.failed);
    });

    test('wrong event and EOF fail closed', () async {
      final messages = ClaimMessages();
      messages.add(utf8.encode('{"event":"ready"}\n'));
      await expectLater(
        messages.read(ClaimEvent.claimed),
        throwsA(
          isA<ClaimProblem>().having(
            (failure) => failure.reason,
            'reason',
            ClaimReason.wrongEvent,
          ),
        ),
      );
      messages.finish();
      await expectLater(
        messages.read(ClaimEvent.claimed),
        throwsA(
          isA<ClaimProblem>().having(
            (failure) => failure.reason,
            'reason',
            ClaimReason.unexpectedEof,
          ),
        ),
      );
    });

    test('queue overflow is latched and cannot discard into a pass', () async {
      final messages = ClaimMessages();
      for (var index = 0; index <= maxClaimPendingMessages; index++) {
        messages.add(utf8.encode('{"event":"ready"}\n'));
      }
      for (var index = 0; index < 2; index++) {
        await expectLater(
          messages.read(ClaimEvent.ready),
          throwsA(
            isA<ClaimProblem>().having(
              (failure) => failure.reason,
              'reason',
              ClaimReason.messageOverflow,
            ),
          ),
        );
      }
      expect(messages.validate, throwsA(isA<ClaimProblem>()));
    });

    test(
      'malformed and oversized child output stay content-free failures',
      () async {
        for (final bytes in [
          utf8.encode('private token\n'),
          List<int>.filled(maxProbeLineBytes + 1, 120),
        ]) {
          final messages = ClaimMessages();
          messages.add(bytes);
          messages.finish();
          messages.add(utf8.encode('{"event":"ready"}\n'));
          await expectLater(
            messages.read(ClaimEvent.ready),
            throwsA(
              isA<ClaimProblem>()
                  .having(
                    (failure) => failure.reason,
                    'reason',
                    ClaimReason.malformedMessage,
                  )
                  .having(
                    (failure) => failure.toString(),
                    'safe text',
                    isNot(contains('private')),
                  ),
            ),
          );
        }
      },
    );

    test('worker primary remains visible beside its closing failure', () {
      final messages = ClaimMessages();
      messages.observeWorkerFailure(
        const ClaimProblem(
          ClaimReason.workerExit,
          workerStage: ClaimWorkerStage.verify,
          workerReason: ClaimReason.outboxRecord,
        ),
      );
      messages.observeWorkerFailure(
        const ClaimProblem(
          ClaimReason.workerExit,
          workerStage: ClaimWorkerStage.closing,
          workerReason: ClaimReason.unexpected,
        ),
      );
      expect(messages.workerFailure?.workerReason, ClaimReason.outboxRecord);
      expect(
        messages.workerCleanupFailure?.workerReason,
        ClaimReason.unexpected,
      );
      expect(
        messages.validate,
        throwsA(
          isA<ClaimProblem>().having(
            (failure) => failure.workerReason,
            'primary',
            ClaimReason.outboxRecord,
          ),
        ),
      );
    });
  });

  test(
    'claim lifecycle is bounded, detached, and omits PIDs and payloads',
    () async {
      var ticks = 0;
      final lifecycle = ClaimLifecycle(
        ClaimMode.actor,
        ClaimSlot.firstActor,
        elapsed: () => Duration(microseconds: ticks++),
      );
      final messages = ClaimMessages();
      messages.add(utf8.encode('{"event":"started","pid":12345}\n'));
      await messages.read(ClaimEvent.started);
      lifecycle.pidRelation = 'different_from_direct';
      lifecycle.workerStage = ClaimWorkerStage.claim;
      for (var index = 0; index < maxProbePhaseRecords * 3; index++) {
        lifecycle.record(ClaimPhase.received);
      }
      final snapshot = lifecycle.snapshot(messages);
      expect(snapshot['truncated'], isTrue);
      expect(
        snapshot['records']! as List<Object?>,
        hasLength(maxProbePhaseRecords),
      );
      expect(snapshot['last_expected_event'], 'started');
      expect(snapshot['last_observed_event'], 'started');
      lifecycle.record(ClaimPhase.cleanup);
      expect(jsonEncode(snapshot), isNot(contains('cleanup')));
      expect(jsonEncode(snapshot), isNot(contains('12345')));
      expect(jsonEncode(snapshot), isNot(contains('private')));
    },
  );
}
