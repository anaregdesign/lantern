import 'package:flutter_test/flutter_test.dart';

import '../../offline/tool/resource_profile.dart';
import '../tool/performance_probe.dart' as probe;

void main() {
  test('bounded SQLite WAL, reopen and automatic replay contracts', () async {
    final result = await probe.sqliteStoreContracts(
      null,
      ResourceProfile.unitContracts,
    );
    expect(result['records'], 8);
    final automatic = result['automatic']! as Map<String, Object?>;
    expect(automatic['items'], 8);
    expect(automatic['traversalSamples'], 3);
  });
}
