import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_example/main.dart';

void main() {
  testWidgets('missing runtime configuration fails closed with guidance', (
    tester,
  ) async {
    await tester.pumpWidget(const LanternExampleApp(configuration: null));

    expect(find.byKey(const Key('configuration-help')), findsOneWidget);
    expect(find.textContaining('LANTERN_ALLOW_INSECURE'), findsOneWidget);
  });
}
