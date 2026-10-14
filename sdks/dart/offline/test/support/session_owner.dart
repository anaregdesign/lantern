import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline_adapter.dart';

/// In-process fixture capability. Tests do not qualify SQLite/process ownership.
final class TestOwner implements OfflineAutomaticOwner {
  @override
  int epoch = 1;
  bool current = true;
  @override
  void checkCurrent() {
    if (!current) throw const LanternDataException(DataFailure.closed);
  }

  @override
  Future<void> release() async => current = false;
}
