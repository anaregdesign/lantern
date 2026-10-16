import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:lantern_example/app_data_session.dart';
import 'package:lantern_example/native_credential_store.dart';
import 'package:lantern_example/native_session_controller.dart';

import 'native_test_helpers.dart';
import 'app_data_test_support.dart' show AppClock;

void main() {
  for (final phase in ['open', 'write']) {
    for (final transition in ['cancel', 'switch', 'close']) {
      test(
        'R1 $transition joins held $phase and fences provisional work',
        () async {
          final clock = AppClock()..now = DateTime.now().toUtc();
          final store = MemoryCredentials(), providers = Providers();
          final wire = NativeWire(store);
          final held = Completer<void>(), reached = Completer<void>();
          final cleanup = Completer<void>(), cleanupSeen = Completer<void>();
          if (phase == 'write') store.holdWrite = held;
          final opened = <String>[];
          AppDataSession? provisional;
          LanternClient? firstClient;
          final c = NativeSessionController(
            endpoint: endpoint,
            providers: providers,
            store: store,
            createClient: (credential) {
              final client = LanternClient.connect(
                endpoint,
                transport: wire,
                tokenProvider: () => credential,
                onClose: () async {
                  if (credential != null && !cleanupSeen.isCompleted) {
                    cleanupSeen.complete();
                    await cleanup.future;
                  }
                },
              );
              if (credential != null) firstClient ??= client;
              return client;
            },
            openData: (client, session, fresh, lifetime) async {
              opened.add(session.principal.subject);
              final data = await AppDataSessionFactory(
                client: client,
                binding: LanternSessionBinding.native(
                  session,
                  securityDomain: 'provisional-lifetime',
                ),
                profile: AppDataProfile.memory,
                legacyPartition: 'unused',
                lifetime: lifetime,
                clock: clock.call,
                schedule: clock.schedule,
              ).open(activate: false);
              if (session.principal.subject == 'a') {
                provisional = data;
                // Existing local intent is eligible, but startup cannot send it.
                await data.data!.mutations.submit(
                  MutationRequest.putVertices([
                    VertexInput(
                      key: 'private:pending',
                      value: const StringValue('a'),
                    ),
                  ]),
                );
                reached.complete();
                if (phase == 'open') await held.future;
              }
              return data;
            },
          );
          addTearDown(() async {
            if (!held.isCompleted) held.complete();
            if (!cleanup.isCompleted) cleanup.complete();
            await c.close();
          });
          final login = c.login(issuer, 'mobile');
          await clock.settle(reached.future);
          if (phase == 'write') await clock.settle(store.writeSeen.future);
          await clock.advance(const Duration(seconds: 1));
          expect(wire.dataDispatches, isEmpty);
          expect(wire.receiptStatuses, isEmpty);
          Future<void>? successor;
          var completed = false;
          switch (transition) {
            case 'cancel':
              c.cancelLogin();
            case 'switch':
              providers.subject = 'b';
              successor = c.login(issuer, 'mobile');
            case 'close':
              successor = c.close();
          }
          successor?.then((_) => completed = true);
          await clock.advance(const Duration(seconds: 1));
          expect(completed, false);
          expect(opened, ['a']);
          expect(providers.authentications, 1);
          expect(c.account, isNull);
          await expectLater(
            () => provisional!.data!.getVertex('private'),
            throwsA(isA<OfflineDisposedException>()),
          );
          expect(wire.dataDispatches, isEmpty);
          expect(wire.receiptStatuses, isEmpty);
          held.complete();
          await clock.settle(cleanupSeen.future);
          await clock.advance(const Duration(seconds: 1));
          expect(
            completed,
            false,
            reason: 'Retirement must also join transport close',
          );
          expect(opened, ['a']);
          expect(c.account, isNull);
          cleanup.complete();
          await clock.settle(login);
          if (successor != null) await clock.settle(successor);
          expect(firstClient!.isClosed, true);
          if (transition == 'switch') {
            expect(opened, ['a', 'b']);
            expect(c.account!.session.principal.subject, 'b');
          } else {
            expect(c.account, isNull);
            expect(store.saved, isNull);
          }
          expect(wire.dataDispatches, isEmpty);
          expect(wire.receiptStatuses, isEmpty);
        },
      );
    }
  }
  test(
    'I6 incomplete startup retirement prevents successor activation',
    () async {
      final store = MemoryCredentials(),
          providers = Providers(),
          wire = NativeWire(MemoryCredentials());
      final failure = AppDataStartupFailure(
        StateError('startup'),
        StackTrace.current,
        StateError('retirement'),
        StackTrace.current,
      );
      var opens = 0;
      final controller = NativeSessionController(
        endpoint: endpoint,
        providers: providers,
        store: store,
        createClient: (credential) => LanternClient.connect(
          endpoint,
          tokenProvider: () => credential,
          transport: wire,
        ),
        openData: (_, _, _, _) async {
          opens++;
          throw failure;
        },
      );
      await controller.login(issuer, 'mobile');
      expect(controller.account, isNull);
      await controller.login(issuer, 'mobile');
      expect(opens, 1);
      expect(providers.authentications, 1);
      await expectLater(controller.close(), throwsA(same(failure)));
    },
  );
  testWidgets(
    'I8 absolute expiry closes account without another provider login',
    (tester) async {
      Future<void> finish(Future<void> work) async {
        var done = false;
        Object? failure;
        work.then<void>(
          (_) {
            done = true;
          },
          onError: (Object e) {
            failure = e;
            done = true;
          },
        );
        for (var turn = 0; turn < 100 && !done; turn++) {
          await tester.pump(const Duration(milliseconds: 1));
          await tester.runAsync(() => Future<void>.delayed(Duration.zero));
        }
        expect(done, true, reason: 'Finite native lifecycle fixture');
        if (failure != null) throw failure!;
      }

      final store = MemoryCredentials(),
          providers = Providers(),
          wire = NativeWire(MemoryCredentials());
      final controller = NativeSessionController(
        endpoint: endpoint,
        providers: providers,
        store: store,
        createClient: (credential) => LanternClient.connect(
          endpoint,
          tokenProvider: () => credential,
          transport: wire,
        ),
        openData: (_, _, _, _) async => null,
      );
      addTearDown(() => finish(controller.close()));
      await finish(controller.login(issuer, 'mobile'));
      final old = controller.account!;
      final deadline = old.session.expiresAt;
      await tester.pump(const Duration(hours: 4));
      await finish(controller.inspect());
      expect(controller.account!.session.expiresAt, deadline);
      await tester.pump(const Duration(hours: 4));
      await finish(Future<void>.delayed(Duration.zero));
      expect(controller.account, isNull);
      expect(old.client.isClosed, true);
      expect(store.saved, isNull);
      expect(providers.authentications, 1);
    },
  );
  test(
    'I8 account replacement joins old data reply and rejects its publication',
    () async {
      final store = MemoryCredentials(),
          providers = Providers(),
          wire = NativeWire(MemoryCredentials());
      final opened = <String>[];
      final controller = NativeSessionController(
        endpoint: endpoint,
        providers: providers,
        store: store,
        createClient: (credential) => LanternClient.connect(
          endpoint,
          tokenProvider: () => credential,
          transport: wire,
        ),
        openData: (client, session, fresh, lifetime) {
          opened.add(session.principal.subject);
          return AppDataSessionFactory(
            lifetime: lifetime,
            client: client,
            binding: LanternSessionBinding.native(
              session,
              securityDomain: 'retirement-test',
            ),
            profile: AppDataProfile.memory,
            legacyPartition: 'unused',
          ).open(activate: false);
        },
      );
      addTearDown(controller.close);
      addTearDown(() {
        if (!wire.releaseDataRead.isCompleted) wire.releaseDataRead.complete();
      });
      await controller.login(issuer, 'mobile');
      final old = controller.account!;
      wire.holdDataRead = true;
      final read = old.dataSession!.data!.getVertex('private');
      final rejected = expectLater(
        read,
        throwsA(isA<OfflineDisposedException>()),
      );
      await wire.dataReadSeen.future.timeout(const Duration(seconds: 5));
      providers.subject = 'b';
      final replacement = controller.login(issuer, 'mobile');
      await Future<void>.delayed(Duration.zero);
      expect(opened, ['a']);
      expect(providers.authentications, 1);
      wire.holdDataRead = false;
      wire.releaseDataRead.complete();
      await rejected;
      await replacement;
      expect(opened, ['a', 'b']);
      expect(old.client.isClosed, true);
      final value = await controller.account!.dataSession!.data!.getVertex(
        'private',
      );
      expect((value.view!.value as StringValue).value, 'private-b');
    },
  );
  test(
    'I6 retirement failure is memoized and blocks successor authentication and data',
    () async {
      final store = MemoryCredentials(),
          plugins = Providers(),
          wire = NativeWire(MemoryCredentials());
      final failure = StateError('account transport close failed');
      var opens = 0, closes = 0;
      final controller = NativeSessionController(
        endpoint: endpoint,
        providers: plugins,
        store: store,
        createClient: (credential) => LanternClient.connect(
          endpoint,
          tokenProvider: () => credential,
          transport: wire,
          onClose: () async {
            if (credential != null) {
              closes++;
              throw failure;
            }
          },
        ),
        openData: (_, _, _, _) async {
          opens++;
          return null;
        },
      );
      await controller.login(issuer, 'mobile');
      final old = controller.account!;
      plugins.subject = 'b';
      await controller.login(issuer, 'mobile');
      expect(controller.account, isNull);
      expect(plugins.authentications, 1);
      expect(opens, 1);
      final closed = old.close();
      expect(identical(closed, old.close()), isTrue);
      await expectLater(closed, throwsA(same(failure)));
      await controller.login(issuer, 'mobile');
      expect(opens, 1);
      expect(closes, 1);
      final controllerClosed = controller.close();
      expect(identical(controllerClosed, controller.close()), isTrue);
      await expectLater(controllerClosed, throwsA(same(failure)));
    },
  );
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
          openData: (_, _, _, _) async => null,
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
    openData: (client, session, fresh, lifetime) async {
      opened.add(nativeAccountPartition(endpoint, session));
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
        openData: (client, session, fresh, lifetime) async {
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
