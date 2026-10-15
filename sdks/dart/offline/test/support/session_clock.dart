import 'dart:async';
import 'package:lantern_client_offline/src/session_runtime.dart';
import 'package:test/test.dart';

final class TestAlarm implements OfflineSessionAlarm {
  TestAlarm(this.at, this.callback);
  final DateTime at;
  final void Function() callback;
  bool canceled = false;
  @override
  void cancel() => canceled = true;
}

final class Clock {
  DateTime now = DateTime.utc(2026, 10, 14);
  final List<TestAlarm> alarms = [];
  int callbacks = 0;
  int get active => alarms.where((a) => !a.canceled).length;
  DateTime call() => now;
  OfflineSessionAlarm schedule(Duration delay, void Function() callback) {
    final alarm = TestAlarm(now.add(delay), callback);
    alarms.add(alarm);
    expect(active, lessThanOrEqualTo(1));
    return alarm;
  }

  Future<void> advance(Duration duration) async {
    now = now.add(duration);
    for (var turn = 0; turn < 100; turn++) {
      final due = alarms
          .where((a) => !a.canceled && !a.at.isAfter(now))
          .toList();
      if (due.isEmpty) {
        await flush();
        if (!alarms.any((a) => !a.canceled && !a.at.isAfter(now))) return;
        continue;
      }
      for (final alarm in due) {
        alarm.canceled = true;
        callbacks++;
        alarm.callback();
      }
      await flush();
    }
    fail('runtime spun an expired wakeup');
  }
}

Future<void> flush() async {
  for (var i = 0; i < 4; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}
