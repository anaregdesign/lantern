import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_example/native_credential_store.dart';
import 'package:lantern_example/native_session_controller.dart';

import 'native_test_helpers.dart';

void main() {
  test(
    'Apple nonauthorized state or local plugin error closes the active account',
    () async {
      for (final failed in [false, true]) {
        final store = MemoryCredentials(), plugins = Providers();
        final wire = NativeWire(
          store,
          providerIssuer: 'https://appleid.apple.com',
        );
        final c = NativeSessionController(
          endpoint: endpoint,
          providers: plugins,
          store: store,
          createClient: (credential) => LanternClient.connect(
            endpoint,
            tokenProvider: () => credential,
            transport: wire,
          ),
          openOffline: (_, _) async => null,
        );
        addTearDown(c.close);
        await c.login('https://appleid.apple.com', 'mobile');
        final old = c.account!;
        plugins.appleUsable = false;
        plugins.appleFails = failed;
        await c.inspect();
        expect(c.account, isNull);
        expect(store.saved, isNull);
        expect(old.client.isClosed, isTrue);
        expect(old.cancellation.isCanceled, isTrue);
      }
    },
  );
  NativeSessionController controller(
    MemoryCredentials store,
    Providers plugins,
    NativeWire wire,
    List<String> opened,
  ) => NativeSessionController(
    endpoint: endpoint,
    providers: plugins,
    store: store,
    createClient: (credential) => LanternClient.connect(
      endpoint,
      tokenProvider: () => credential,
      transport: wire,
    ),
    openOffline: (client, partition) async {
      opened.add(partition);
      return null;
    },
  );

  test(
    'late provider completion cannot install or overwrite a newer account',
    () async {
      final store = MemoryCredentials(),
          plugins = Providers(),
          wire = NativeWire(store),
          opened = <String>[];
      final c = controller(store, plugins, wire, opened);
      addTearDown(c.close);
      final first = plugins.held = Completer<NativeProviderCredential>();
      final pending = c.login(issuer, 'mobile');
      await plugins.called.future;
      c.cancelLogin();
      plugins.held = null;
      plugins.subject = 'b';
      await c.login(issuer, 'mobile');
      expect(c.account?.session.principal.subject, 'b');
      first.complete(const GoogleNativeCredential(idToken: 'a'));
      await pending;
      expect(store.saved?.subject, 'b');
      expect(wire.completions, 1);
      expect(opened, hasLength(1));
    },
  );
  test(
    'account switches close old clients, preserve per-account recovery, and use status first',
    () async {
      final store = MemoryCredentials(),
          plugins = Providers(),
          wire = NativeWire(store),
          opened = <String>[];
      final c = controller(store, plugins, wire, opened);
      addTearDown(c.close);
      await c.login(issuer, 'mobile');
      final a = c.account!;
      plugins.subject = 'b';
      await c.login(issuer, 'mobile');
      final b = c.account!;
      expect(a.client.isClosed, isTrue);
      expect(a.cancellation.isCanceled, isTrue);
      expect(a.partition, isNot(b.partition));
      expect(wire.applies, 1);
      expect(wire.statuses, 0);
      expect(store.records[a.partition], hasLength(1));
      await expectLater(
        a.client.getNativeSession(),
        throwsA(isA<LanternFailedPreconditionException>()),
      );
      plugins.subject = 'a';
      await c.login(issuer, 'mobile');
      expect(c.account!.partition, a.partition);
      expect(wire.applies, 2);
      expect(wire.statuses, 1);
      expect(store.records[a.partition], isEmpty);
      expect(store.records[b.partition], hasLength(1));
      expect(opened, [a.partition, b.partition, a.partition]);
    },
  );
  test(
    'canceling an authority replacement cannot redisplay the old account',
    () async {
      final store = MemoryCredentials(),
          plugins = Providers(),
          wire = NativeWire(store),
          opened = <String>[];
      final c = controller(store, plugins, wire, opened);
      addTearDown(c.close);
      await c.login(issuer, 'mobile');
      final old = c.account!;
      wire.authorityBinding++;
      wire.holdSessionRead = 3;
      final inspecting = c.inspect();
      await wire.sessionBlocked.future;
      c.cancelLogin();
      expect(c.account, isNull);
      wire.releaseSession.complete();
      await inspecting;
      await c.close();
      expect(old.client.isClosed, isTrue);
      expect(old.cancellation.isCanceled, isTrue);
      expect(c.account, isNull);
      expect(c.busy, isFalse);
      expect(wire.applies, 0);
    },
  );
  test(
    'policy replacement failure closes old work and leaves no busy account',
    () async {
      final store = MemoryCredentials(),
          plugins = Providers(),
          wire = NativeWire(store);
      var opens = 0;
      final c = NativeSessionController(
        endpoint: endpoint,
        providers: plugins,
        store: store,
        createClient: (credential) => LanternClient.connect(
          endpoint,
          tokenProvider: () => credential,
          transport: wire,
        ),
        openOffline: (client, partition) async {
          if (++opens == 2) throw StateError('Storage unavailable');
          return null;
        },
      );
      addTearDown(c.close);
      await c.login(issuer, 'mobile');
      final old = c.account!;
      wire.authorityBinding++;
      await c.inspect();
      expect(old.client.isClosed, isTrue);
      expect(old.cancellation.isCanceled, isTrue);
      expect(c.account, isNull);
      expect(c.busy, isFalse);
      expect(store.saved, isNull);
      expect(wire.applies, 0);
    },
  );
  test(
    'restore verifies exact account and expiry without invoking a provider',
    () async {
      final store = MemoryCredentials(),
          plugins = Providers(),
          wire = NativeWire(store),
          opened = <String>[];
      final c = controller(store, plugins, wire, opened);
      await c.login(issuer, 'mobile');
      await c.close();
      final restored = controller(store, plugins, wire, opened);
      addTearDown(restored.close);
      await restored.restore();
      expect(restored.account?.session.principal.subject, 'a');
      expect(plugins.authentications, 1);
      await restored.close();
      store.saved = StoredNativeCredential(
        credential: store.saved!.credential,
        issuer: issuer,
        subject: 'other',
        profile: 'mobile',
        expiresAt: store.saved!.expiresAt,
      );
      final mixed = controller(store, plugins, wire, opened);
      addTearDown(mixed.close);
      await mixed.restore();
      expect(mixed.account, isNull);
      expect(plugins.authentications, 1);
    },
  );
}
