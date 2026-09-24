import 'dart:async';
import 'dart:convert';
import 'dart:io';

const _timeout = Duration(seconds: 20);

/// Exercises independent SQLite claimers, owner death, CAS, and lease recovery.
Future<void> main() async {
  try {
    stdout.writeln(jsonEncode(await runClaimProbe()));
  } catch (_) {
    stderr.writeln('claim_probe_failed');
    exitCode = 1;
  }
}

/// Runs the claim proof for the existing crash gate and returns aggregate data.
Future<Map<String, Object?>> runClaimProbe() async {
  if (Platform.isWindows) {
    throw StateError('claim_probe_requires_posix_sigkill');
  }
  final directory = await Directory.systemTemp.createTemp('sqlite-claim-');
  final path = '${directory.path}/store.db';
  final peers = <_Peer>[];
  var stage = 'seed';
  try {
    await _once('seed', path, 'a', 'seeded');
    stage = 'open_claimers';
    peers.add(await _Peer.start(path, 'a'));
    peers.add(await _Peer.start(path, 'b'));
    stage = 'competing_claims';
    // Both independent VMs and native SQLite handles are open before this
    // barrier. They share neither the adapter's in-isolate lane nor heap state.
    await Future.wait(peers.map((peer) => peer.send('claim')));
    final results = await Future.wait(
      peers.map((peer) => peer.read('claimed')),
    );
    final counts = results.map((result) => result['count']).toList();
    _require(
      counts.where((count) => count == 1).length == 1 &&
          counts.where((count) => count == 0).length == 1,
    );
    final winner = peers[counts.indexOf(1)];
    final other = peers[counts.indexOf(0)];
    stage = 'renew_cas';
    await other.send('renew');
    _require((await other.read('renewed'))['accepted'] == false);
    await winner.send('renew');
    _require((await winner.read('renewed'))['accepted'] == true);
    stage = 'kill_owner';
    // Kill with the acknowledged lease still durable and its database open.
    await winner.kill();
    stage = 'durable_renewal_after_owner_death';
    await other.send('before-expiry');
    _require((await other.read('blocked'))['count'] == 0);
    stage = 'expired_lease_recovery';
    await other.send('recover');
    _require((await other.read('recovered'))['count'] == 1);
    await other.send('stale-renew');
    _require((await other.read('stale-renewed'))['accepted'] == false);
    await other.send('verify');
    await other.read('verified');
    await other.stop();
    stage = 'fresh_process_verify';
    await _once('verify', path, other.owner, 'verified');
    return {
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
  } catch (_) {
    throw StateError('claim_probe_failed:$stage');
  } finally {
    for (final peer in peers) {
      await peer.dispose();
    }
    await directory.delete(recursive: true);
  }
}

Future<Process> _start(String mode, String path, String owner) =>
    Process.start(Platform.resolvedExecutable, [
      'run',
      File.fromUri(Platform.script.resolve('claim_worker.dart')).path,
      mode,
      path,
      owner,
    ]);

Future<void> _once(String mode, String path, String owner, String event) async {
  final peer = await _Peer.start(path, owner, mode: mode);
  try {
    await peer.read(event);
    await peer.complete();
  } finally {
    await peer.dispose();
  }
}

final class _Peer {
  _Peer(this.process, this.owner)
    : lines = StreamIterator(
        process.stdout.transform(utf8.decoder).transform(const LineSplitter()),
      ),
      errors = process.stderr.drain<void>();
  final Process process;
  final String owner;
  final StreamIterator<String> lines;
  final Future<void> errors;
  int? workerPid;
  bool finished = false;
  bool workerKilled = false;

  static Future<_Peer> start(
    String path,
    String owner, {
    String mode = 'actor',
  }) async {
    final peer = _Peer(await _start(mode, path, owner), owner);
    try {
      // Capture the actual VM before it can block opening a locked database.
      final started = await peer.read('started');
      peer.workerPid = started['pid']! as int;
      _require(peer.workerPid! > 0);
      if (mode == 'actor') await peer.read('ready');
      return peer;
    } catch (_) {
      await peer.dispose();
      rethrow;
    }
  }

  Future<void> send(String command) async {
    process.stdin.writeln(command);
    await process.stdin.flush();
  }

  Future<Map<String, Object?>> read(String event) async {
    _require(await lines.moveNext().timeout(_timeout));
    final result = jsonDecode(lines.current) as Map<String, Object?>;
    _require(result['event'] == event);
    return result;
  }

  Future<void> kill() async {
    _require(Process.killPid(workerPid!, ProcessSignal.sigkill));
    workerKilled = true;
    final code = await process.exitCode.timeout(_timeout);
    finished = true;
    _require(code != 0);
    await errors.timeout(_timeout);
  }

  Future<void> stop() async {
    await send('stop');
    await read('stopped');
    await complete();
  }

  Future<void> complete() async {
    final code = await process.exitCode.timeout(_timeout);
    finished = true;
    _require(code == 0);
    await errors.timeout(_timeout);
  }

  Future<void> dispose() async {
    if (!finished) {
      if (workerPid != null && !workerKilled) {
        Process.killPid(workerPid!, ProcessSignal.sigkill);
        workerKilled = true;
      }
      process.kill(ProcessSignal.sigkill);
      await process.exitCode.timeout(_timeout);
      await errors.timeout(_timeout);
    }
    await lines.cancel().timeout(_timeout);
  }
}

void _require(bool condition) {
  if (!condition) throw StateError('claim_contract');
}
