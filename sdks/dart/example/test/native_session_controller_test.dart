import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:connectrpc/connect.dart' as connect;
import 'package:connectrpc/protobuf.dart' as codec;
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_example/native_credential_store.dart';
import 'package:lantern_example/native_providers.dart';
import 'package:lantern_example/native_session_controller.dart';

const issuer = 'https://accounts.google.com';
final endpoint = Uri.parse('https://example.test');
String random(int n) => base64UrlEncode(List.filled(32, n)).replaceAll('=', '');
String bytes(int n, [int size = 32]) => base64Encode(List.filled(size, n));

final class MemoryCredentials implements NativeCredentialStore {
  StoredNativeCredential? saved;
  final records = <String, List<NativeLogoutReview>>{};
  @override
  Future<StoredNativeCredential?> read() async => saved;
  @override
  Future<void> write(StoredNativeCredential value) async {
    saved = value;
  }

  @override
  Future<void> clear() async {
    saved = null;
  }

  @override
  Future<List<NativeLogoutReview>> pending(String p) async =>
      List.of(records[p] ?? []);
  @override
  Future<void> savePending(String p, List<NativeLogoutReview> r) async {
    records[p] = List.of(r);
  }
}

final class Providers implements NativeProviders {
  String subject = 'a';
  Completer<NativeProviderCredential>? held;
  final called = Completer<void>();
  var authentications = 0;
  @override
  Future<NativeProviderCredential> authenticate(
    NativePluginConfiguration c, {
    bool lightweight = false,
  }) async {
    authentications++;
    if (!called.isCompleted) called.complete();
    if (held case final pending?) return pending.future;
    return GoogleNativeCredential(idToken: subject);
  }

  @override
  Future<void> signOutGoogle() async {}
  @override
  Future<bool> appleCredentialUsable(String subject) async => true;
}

// Uses the public Connect codec/transport only. The app and its tests never
// import generated SDK classes; JSON here is explicit wire fixture evidence.
final class NativeWire implements connect.Transport {
  NativeWire(this.store);
  final MemoryCredentials store;
  final now = DateTime.now().toUtc().subtract(const Duration(minutes: 1));
  final subjects = <String, String>{};
  int completions = 0, applies = 0, statuses = 0;
  int authorityBinding = 13;
  int sessionReads = 0, holdSessionRead = 0;
  final sessionBlocked = Completer<void>(), releaseSession = Completer<void>();
  final sent = <String?>[];
  final profile = <String, Object>{
    'version': 2,
    'domain': bytes(1),
    'cohort': bytes(2),
    'generation': bytes(3, 16),
    'protocol': bytes(4),
    'timeProfile': bytes(5),
    'membership': bytes(6),
    'configuration': bytes(7),
  };
  Map<String, Object> get cut => {
    'version': 1,
    'domain': bytes(1),
    'cohort': bytes(2),
    'generation': bytes(3, 16),
    'sequence': '1',
    'previous': bytes(8),
    'projection': bytes(9),
    'frontier': bytes(10),
    'fences': bytes(11),
    'policy': bytes(12),
  };
  Map<String, Object> identity(String subject) => {
    'kind': 1,
    'issuer': issuer,
    'subject': subject,
  };
  Map<String, Object> session(String subject) => {
    'credentialProfile': 'native_end_user',
    'clientProfileId': 'mobile',
    'serviceOrigin': endpoint.origin,
    'identity': identity(subject),
    'createdAt': now.toIso8601String(),
    'expiresAt': now.add(const Duration(hours: 8)).toIso8601String(),
    'version': {
      'currentProfile': profile,
      'currentCut': cut,
      'admissionBinding': bytes(authorityBinding),
    },
  };
  @override
  Future<connect.UnaryResponse<I, O>> unary<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    I input, [
    connect.CallOptions? options,
  ]) async {
    const json = codec.JsonCodec();
    final req =
        jsonDecode(utf8.decode(json.encode(input))) as Map<String, dynamic>;
    final authorization = options?.headers?['authorization'];
    sent.add(authorization);
    final credential = authorization?.replaceFirst('Bearer ', '');
    final subject = subjects[credential] ?? '';
    Map<String, Object?> response;
    switch (spec.procedure.split('/').last) {
      case 'BeginNativeLogin':
        expect(authorization, isNull);
        response = {
          'attemptId': random(1),
          'completionSecret': random(2),
          'affinity': 'v2.1.${random(3)}',
          'expiresAt': now.add(const Duration(minutes: 10)).toIso8601String(),
          'configuration': {
            'kind': 1,
            'clientProfileId': 'mobile',
            'clientId': 'backend',
            'serviceOrigin': endpoint.origin,
          },
        };
      case 'CompleteNativeLogin':
        expect(authorization, isNull);
        completions++;
        final subject =
            (req['google'] as Map<String, dynamic>)['idToken'] as String;
        final issued = 'lnu1_${random(20 + completions)}';
        subjects[issued] = subject;
        response = {'credential': issued, 'session': session(subject)};
      case 'GetNativeSession':
        if (++sessionReads == holdSessionRead) {
          sessionBlocked.complete();
          await releaseSession.future;
        }
        if (subject.isEmpty) {
          throw connect.ConnectException(
            connect.Code.unauthenticated,
            'unknown',
          );
        }
        response = {'session': session(subject)};
      case 'PrepareNativeLogout':
        response = {
          'review': {
            'profile': profile,
            'expectedCut': cut,
            'changeId': {
              'version': 1,
              'domain': bytes(1),
              'cohort': bytes(2),
              'namespace': '1',
              'nonce': bytes(31 + applies, 16),
            },
            'actor': identity(subject),
            'intentDigest': bytes(30),
            'sessionDigest': List.filled(64, 'a').join(),
            'sessionLineage': '1',
          },
        };
      case 'ApplyNativeLogout':
        expect(
          store.records.values.expand((r) => r),
          isNotEmpty,
          reason: 'retain review before dispatch',
        );
        applies++;
        throw connect.ConnectException(
          connect.Code.unavailable,
          'response lost',
        );
      case 'GetSecurityChangeStatus':
        statuses++;
        response = {
          'currentResult': {
            'profile': req['currentProfile'],
            'changeId': req['currentChangeId'],
            'intentDigest': req['currentIntentDigest'],
            'original': {
              'changeId': req['currentChangeId'],
              'intentDigest': req['currentIntentDigest'],
              'disposition': 1,
            },
          },
        };
      default:
        throw StateError('Unexpected test RPC');
    }
    final output = spec.outputFactory();
    json.decode(Uint8List.fromList(utf8.encode(jsonEncode(response))), output);
    return connect.UnaryResponse(
      spec,
      connect.Headers(),
      output,
      connect.Headers(),
    );
  }

  @override
  Future<connect.StreamResponse<I, O>>
  stream<I extends Object, O extends Object>(
    connect.Spec<I, O> spec,
    Stream<I> input, [
    connect.CallOptions? options,
  ]) => throw UnimplementedError();
}

void main() {
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
