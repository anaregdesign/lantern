import 'dart:io';

import 'package:lantern_client_offline/lantern_client_offline.dart';

Future<void> main(List<String> arguments) async {
  if (arguments.length != 1) {
    exitCode = 64;
    return;
  }
  final snapshot = await File(arguments.single).readAsString();
  final store = InMemoryOfflineStore.fromSnapshot(snapshot);
  stdout.write(await store.exportSnapshot());
}
