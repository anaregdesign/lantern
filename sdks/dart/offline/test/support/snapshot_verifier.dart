import 'dart:io';

import 'package:lantern_client_offline/lantern_client_offline.dart';

Future<void> main(List<String> arguments) async {
  if (arguments.length != 1 &&
      !(arguments.length == 4 && arguments.first == '--recover-lease')) {
    exitCode = 64;
    return;
  }
  final path = arguments.length == 1 ? arguments.single : arguments[1];
  final snapshot = await File(path).readAsString();
  final store = InMemoryOfflineStore.fromSnapshot(snapshot);
  if (arguments.length == 4) {
    final nowMicros = int.tryParse(arguments[2]);
    final owner = arguments[3];
    if (nowMicros == null || owner.isEmpty) {
      exitCode = 64;
      return;
    }
    final now = DateTime.fromMicrosecondsSinceEpoch(nowMicros, isUtc: true);
    final claimed = await store.transaction(
      (transaction) => transaction.claim(
        'p',
        owner: owner,
        now: now,
        maxAge: const Duration(days: 1),
        leaseDuration: const Duration(seconds: 2),
        limit: 1,
      ),
    );
    if (claimed.length != 1) {
      stderr.writeln('snapshot_process_harness:lease_recovery');
      exitCode = 65;
      return;
    }
  }
  stdout.write(await store.exportSnapshot());
}
