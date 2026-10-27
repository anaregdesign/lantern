import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'claim_protocol.dart';
import 'probe_protocol.dart';

const _timeout = Duration(seconds: 20);

/// Exercises independent SQLite claimers, owner death, CAS, and lease recovery.
Future<void> main() async {
  try {
    stdout.writeln(jsonEncode(await runClaimProbe()));
  } on ClaimProbeFailure catch (error) {
    stderr.writeln('claim_probe_failed:${error.code}');
    exitCode = 1;
  } on Object {
    stderr.writeln('claim_probe_failed:unexpected');
    exitCode = 1;
  }
}

/// Runs the claim proof for the existing crash gate and returns aggregate data.
Future<Map<String, Object?>> runClaimProbe() async {
  if (Platform.isWindows) {
    throw ClaimProbeFailure(ClaimStage.seed, ClaimReason.contract);
  }
  late Directory directory;
  try {
    directory = await Directory.systemTemp.createTemp('sqlite-claim-');
  } on Object catch (error) {
    throw ClaimProbeFailure(ClaimStage.seed, claimReason(error));
  }
  final path = '${directory.path}/store.db';
  final peers = <_Peer>[];
  var stage = ClaimStage.seed;
  try {
    return await withClaimCleanup(
      stage: () => stage,
      body: () async {
        await _once(
          ClaimMode.seed,
          ClaimSlot.seed,
          path,
          'a',
          ClaimEvent.seeded,
          () => stage,
        );
        stage = ClaimStage.openClaimers;
        peers.add(
          await _Peer.start(path, 'a', ClaimSlot.firstActor, () => stage),
        );
        peers.add(
          await _Peer.start(path, 'b', ClaimSlot.secondActor, () => stage),
        );
        stage = ClaimStage.competingClaims;
        // Both independent VMs and native SQLite handles are open before this
        // barrier. They share neither the adapter's in-isolate lane nor heap state.
        await Future.wait(peers.map((peer) => peer.send(ClaimEvent.claimed)));
        final results = await Future.wait(
          peers.map((peer) => peer.read(ClaimEvent.claimed)),
        );
        final counts = results.map((result) => result.count).toList();
        _require(
          counts.where((count) => count == 1).length == 1 &&
              counts.where((count) => count == 0).length == 1,
          ClaimReason.singleWinner,
        );
        final winner = peers[counts.indexOf(1)];
        final other = peers[counts.indexOf(0)];
        stage = ClaimStage.renewCas;
        await other.send(ClaimEvent.renewed);
        _require(
          (await other.read(ClaimEvent.renewed)).accepted == false,
          ClaimReason.nonOwnerRenew,
        );
        await winner.send(ClaimEvent.renewed);
        _require(
          (await winner.read(ClaimEvent.renewed)).accepted == true,
          ClaimReason.ownerRenew,
        );
        stage = ClaimStage.killOwner;
        // Kill with the acknowledged lease still durable and its database open.
        await winner.kill();
        stage = ClaimStage.durableRenewalAfterOwnerDeath;
        await other.send(ClaimEvent.blocked);
        _require(
          (await other.read(ClaimEvent.blocked)).count == 0,
          ClaimReason.beforeExpiryBlocked,
        );
        stage = ClaimStage.expiredLeaseRecovery;
        await other.send(ClaimEvent.recovered);
        _require(
          (await other.read(ClaimEvent.recovered)).count == 1,
          ClaimReason.recoveredOnce,
        );
        await other.send(ClaimEvent.staleRenewed);
        _require(
          (await other.read(ClaimEvent.staleRenewed)).accepted == false,
          ClaimReason.staleRenew,
        );
        await other.send(ClaimEvent.verified);
        await other.read(ClaimEvent.verified);
        await other.stop();
        stage = ClaimStage.freshProcessVerify;
        await _once(
          ClaimMode.verify,
          ClaimSlot.verifier,
          path,
          other.owner,
          ClaimEvent.verified,
          () => stage,
        );
        return <String, Object?>{
          'content_free': true,
          'independent_process_claimers': 2,
          'single_winner': true,
          'lease_renew_cas': true,
          'owner_sigkill': true,
          'renewal_survived_owner_death': true,
          'expired_lease_recovery': true,
          'stale_owner_rejected': true,
          'fresh_process_verified': true,
        };
      },
      cleanups: () => [
        for (final peer in peers) peer.dispose,
        () async {
          await directory.delete(recursive: true);
        },
      ],
    );
  } on ClaimProbeFailure catch (error) {
    stderr.writeln('claim_probe_failure:${jsonEncode(error.snapshot())}');
    rethrow;
  }
}

Future<void> _once(
  ClaimMode mode,
  ClaimSlot slot,
  String path,
  String owner,
  ClaimEvent event,
  ClaimStage Function() stage,
) async {
  final peer = await _Peer.start(path, owner, slot, stage, mode: mode);
  await withClaimCleanup<void>(
    stage: stage,
    body: () async {
      await peer.read(event);
      await peer.complete();
    },
    cleanups: () => [peer.dispose],
  );
}

void _logLifecycle(ClaimLifecycle lifecycle, [_Peer? peer]) => stderr.writeln(
  'claim_probe_child_lifecycle:${jsonEncode({
    ...lifecycle.snapshot(peer?.messages),
    if (peer != null) 'collectors': {'stdout': peer.output.snapshot(), 'stderr': peer.errors.snapshot()},
  })}',
);

final class _Peer {
  _Peer(this.process, this.owner, this.lifecycle) {
    output = ProbeCollector<Object?>(
      process.stdout,
      onData: messages.add,
      onDone: () {
        messages.finish();
        return null;
      },
    );
    final diagnosticLines = ProbeJsonLines(
      onMessage: (message) {
        final stage = parseClaimWorkerPhase(message);
        if (stage != null) {
          lifecycle.workerStage = stage;
          lifecycle.record(ClaimPhase.workerPhase);
          return;
        }
        if (message['event'] == 'failed') {
          try {
            final failure = parseClaimMessage(message).failure;
            if (failure != null) {
              messages.observeWorkerFailure(failure);
            }
          } on ClaimProblem {
            /* Discard untrusted stderr without retaining text. */
          }
        }
      },
      onInvalid: () {},
    );
    errors = ProbeCollector<Object?>(
      process.stderr,
      onData: diagnosticLines.add,
      onDone: () {
        diagnosticLines.finish();
        return null;
      },
    );
  }
  final Process process;
  final String owner;
  final ClaimLifecycle lifecycle;
  final messages = ClaimMessages();
  late final ProbeCollector<Object?> output;
  late final ProbeCollector<Object?> errors;
  int? workerPid;
  bool finished = false;
  bool workerKilled = false;

  static Future<_Peer> start(
    String path,
    String owner,
    ClaimSlot slot,
    ClaimStage Function() stage, {
    ClaimMode mode = ClaimMode.actor,
  }) async {
    final lifecycle = ClaimLifecycle(mode, slot);
    _logLifecycle(lifecycle);
    late Process process;
    try {
      process = await Process.start(Platform.resolvedExecutable, [
        'run',
        File.fromUri(Platform.script.resolve('claim_worker.dart')).path,
        mode.name,
        path,
        owner,
      ]);
    } on Object {
      _logLifecycle(lifecycle);
      throw const ClaimProblem(ClaimReason.spawn);
    }
    lifecycle.record(ClaimPhase.spawned);
    final peer = _Peer(process, owner, lifecycle);
    var admitted = false;
    return withClaimCleanup(
      stage: stage,
      body: () async {
        // Capture the actual VM before it can block opening a locked database.
        final started = await peer.read(ClaimEvent.started);
        peer.workerPid = checkedClaimWorkerPid(started.pid!);
        lifecycle.pidRelation = peer.workerPid == process.pid
            ? 'same_as_direct'
            : 'different_from_direct';
        if (mode == ClaimMode.actor) {
          await peer.read(ClaimEvent.ready);
        }
        admitted = true;
        return peer;
      },
      cleanups: () => [if (!admitted) peer.dispose],
    );
  }

  Future<T> wait<T>(Future<T> pending) async {
    try {
      return await pending.timeout(_timeout);
    } on TimeoutException {
      lifecycle.record(ClaimPhase.timeout);
      rethrow;
    }
  }

  Future<void> send(ClaimEvent response) async {
    final command = switch (response) {
      ClaimEvent.claimed => 'claim',
      ClaimEvent.renewed => 'renew',
      ClaimEvent.blocked => 'before-expiry',
      ClaimEvent.recovered => 'recover',
      ClaimEvent.staleRenewed => 'stale-renew',
      ClaimEvent.verified => 'verify',
      ClaimEvent.stopped => 'stop',
      _ => throw const ClaimProblem(ClaimReason.command),
    };
    lifecycle.record(ClaimPhase.sent);
    process.stdin.writeln(command);
    await process.stdin.flush();
  }

  Future<ClaimMessage> read(ClaimEvent event) async {
    final message = await wait(messages.read(event));
    lifecycle.record(ClaimPhase.received);
    return message;
  }

  bool signal(String phase, bool Function() action) {
    lifecycle.record(ClaimPhase.kill);
    try {
      final accepted = action();
      lifecycle.kills[phase] = accepted ? 'accepted' : 'refused';
      return accepted;
    } on Object {
      lifecycle.kills[phase] = 'error';
      rethrow;
    }
  }

  Future<void> kill() async {
    _require(
      signal('owner', () => Process.killPid(workerPid!, ProcessSignal.sigkill)),
      ClaimReason.killRefused,
    );
    workerKilled = true;
    final code = await wait(process.exitCode);
    finished = true;
    lifecycle.exit = code;
    lifecycle.record(ClaimPhase.exited);
    _require(code != 0, ClaimReason.workerExit);
    await wait(errors.done);
    _require(errors.qualified, ClaimReason.collector);
  }

  Future<void> stop() async {
    await send(ClaimEvent.stopped);
    await read(ClaimEvent.stopped);
    await complete();
  }

  Future<void> complete() async {
    final code = await wait(process.exitCode);
    finished = true;
    lifecycle.exit = code;
    lifecycle.record(ClaimPhase.exited);
    _require(code == 0, ClaimReason.workerExit);
    await wait(errors.done);
    _require(errors.qualified, ClaimReason.collector);
  }

  Future<void> dispose() async {
    lifecycle.record(ClaimPhase.cleanup);
    ClaimReason? failure;
    Future<void> attempt(Future<void> Function() action) async {
      lifecycle.cleanupAttempted++;
      try {
        await action();
        lifecycle.cleanupSucceeded++;
      } on Object catch (error) {
        final reason = claimReason(error);
        lifecycle.cleanupFailures.add(reason);
        failure ??= reason;
      }
    }

    if (!finished) {
      if (workerPid != null && !workerKilled) {
        await attempt(() async {
          signal(
            'worker_cleanup',
            () => Process.killPid(workerPid!, ProcessSignal.sigkill),
          );
        });
        workerKilled = true;
      }
      await attempt(() async {
        signal('direct_cleanup', () => process.kill(ProcessSignal.sigkill));
      });
      await attempt(() async {
        final code = await wait(process.exitCode);
        lifecycle.exit = code;
        lifecycle.record(ClaimPhase.exited);
        finished = true;
      });
    }
    await attempt(() async {
      await wait(Future.wait([output.done, errors.done]));
    });
    await attempt(() async {
      await wait(
        Future.wait([output.cancelIncomplete(), errors.cancelIncomplete()]),
      );
    });
    await attempt(() async {
      _require(output.qualified && errors.qualified, ClaimReason.collector);
      messages.validate();
    });
    _logLifecycle(lifecycle, this);
    if (failure != null) {
      throw ClaimProblem(failure!);
    }
  }
}

/// Preserve the existing positive-PID guard before acquiring a cleanup target.
int checkedClaimWorkerPid(int value) {
  _require(value > 0, ClaimReason.invalidPid);
  return value;
}

void _require(bool condition, ClaimReason reason) {
  if (!condition) {
    throw ClaimProblem(reason);
  }
}
