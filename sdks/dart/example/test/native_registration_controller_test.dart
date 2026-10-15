import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_example/native_credential_store.dart';
import 'package:lantern_example/native_session_controller.dart';

import 'native_test_helpers.dart';

void main() {
  NativeSessionController controller(
    MemoryCredentials store,
    Providers providers,
    NativeWire wire,
  ) => NativeSessionController(
    endpoint: endpoint,
    providers: providers,
    store: store,
    createClient: (credential) => LanternClient.connect(
      endpoint,
      transport: wire,
      tokenProvider: () => credential,
    ),
    openData: (_, _, _) async => null,
  );

  test(
    'signup returns installed UID, re-login and restoration retain same partition and UID',
    () async {
      final store = MemoryCredentials(), providers = Providers();
      final wire = NativeWire(store);
      final app = controller(store, providers, wire);
      addTearDown(app.close);
      await app.register(issuer, 'mobile');
      final original = app.account!;
      expect(original.session.userId, wire.uid('a'));
      expect(original.session.personalPrefix, 'users:${wire.uid('a')}:');
      expect(store.registrationRecords, isEmpty);
      expect(wire.registrationApplies, 1);
      await app.signOut();
      await app.login(issuer, 'mobile');
      expect(app.account!.session.userId, original.session.userId);
      expect(app.account!.partition, original.partition);
      await app.close();
      final restarted = controller(store, providers, wire);
      addTearDown(restarted.close);
      await restarted.restore();
      expect(restarted.account!.session.userId, original.session.userId);
      expect(restarted.account!.partition, original.partition);
      expect(wire.registrationApplies, 1);
    },
  );

  test(
    'lost Apply survives A-B-A and restart; another identity cannot recover or replay it',
    () async {
      final store = MemoryCredentials(), providers = Providers();
      final wire = NativeWire(store)..loseRegistrationResponse = true;
      final app = controller(store, providers, wire);
      await app.register(issuer, 'mobile');
      expect(app.account, isNull);
      final original = store.registrationRecords.single;
      final bytes = original.toBytes(),
          partition = nativeRegistrationPartition(original);
      await app.close();
      final restarted = controller(store, providers, wire);
      addTearDown(restarted.close);
      await restarted.restore();
      expect(restarted.registrationReviews.single.toBytes(), bytes);
      providers.subject = 'b';
      await restarted.recoverRegistration(original);
      expect(wire.registrationStatuses, 0);
      expect(store.registrationRecords.single.toBytes(), bytes);
      await restarted.login(issuer, 'mobile');
      expect(restarted.account!.partition, isNot(partition));
      expect(restarted.account!.session.userId, isNull);
      providers.subject = 'a';
      // A new explicit signup verifies A, discovers its retained original and
      // stops before another Apply, even though its provider input is fresh.
      await restarted.register(issuer, 'mobile');
      expect(wire.registrationApplies, 1);
      expect(store.registrationRecords.single.toBytes(), bytes);
      wire.registrationUnknown = true;
      await restarted.recoverRegistration(original);
      expect(store.registrationRecords.single.toBytes(), bytes);
      expect(wire.registrationApplies, 1);
      wire.registrationUnknown = false;
      await restarted.recoverRegistration(original);
      expect(store.registrationRecords, isEmpty);
      expect(
        restarted.account,
        isNull,
        reason: 'status must not recover a raw session',
      );
      expect(restarted.message, contains('Sign in'));
      await restarted.login(issuer, 'mobile');
      expect(restarted.account!.partition, partition);
      expect(
        restarted.account!.session.personalPrefix,
        'users:${wire.uid('a')}:',
      );
      expect((wire.registrationApplies, wire.registrationStatuses), (1, 2));
    },
  );
}
