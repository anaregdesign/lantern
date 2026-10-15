import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:lantern_client/lantern_client.dart';
import 'package:lantern_client_offline_sqlite/lantern_client_offline_sqlite.dart';
import 'package:lantern_example/installation_policy.dart';
import 'package:sqflite_common_ffi/sqflite_ffi.dart';

// Host-only channel contract fixtures. They do not attest device backup/restore.
void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  sqfliteFfiInit();
  const secureChannel = MethodChannel(
    'plugins.it_nomads.com/flutter_secure_storage',
  );
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  final binding = LanternSessionBinding.application(
    serviceOrigin: Uri.parse('http://localhost:1'),
    securityDomain: 'platform-host-fixture',
    accountIdentity: 'fixture',
    authorityBinding: 'authority',
  );
  for (final platform in [TargetPlatform.android, TargetPlatform.iOS]) {
    test(
      'I5/I8 $platform fresh/reopen/missing/mismatch/exclusion policy',
      () async {
        debugDefaultTargetPlatformOverride = platform;
        final directory = await Directory.systemTemp.createTemp(
          'lantern-installation-contract-',
        );
        final native = _NativePolicyFixture(directory.path);
        String? marker;
        var markerWrites = 0;
        messenger.setMockMethodCallHandler(
          MobileInstallationPolicy.channel,
          native.call,
        );
        messenger.setMockMethodCallHandler(secureChannel, (call) async {
          final args = call.arguments as Map;
          expect(args['key'], 'lantern.offline.installation.v1');
          final options = args['options'] as Map;
          expect(options['accessibility'], 'unlocked_this_device');
          expect(options['synchronizable'], 'false');
          if (call.method == 'read') return marker;
          if (call.method == 'write') {
            markerWrites++;
            marker = args['value'] as String;
            return null;
          }
          fail('Unexpected secure storage operation ${call.method}');
        });
        addTearDown(() async {
          messenger.setMockMethodCallHandler(
            MobileInstallationPolicy.channel,
            null,
          );
          messenger.setMockMethodCallHandler(secureChannel, null);
          debugDefaultTargetPlatformOverride = null;
          await directory.delete(recursive: true);
        });
        Future<SqliteSessionStartup> open() async {
          final location = await MobileInstallationPolicy().resolve(
            binding: binding,
            legacyPartition: 'old-fixture',
          );
          final client = LanternClient.connect(
            binding.serviceOrigin,
            allowInsecure: true,
          );
          addTearDown(client.close);
          return SqliteOfflineStore.openSession(
            path: location.path,
            client: client,
            binding: binding,
            installationPolicy: location.policy,
            adoption: location.adoption,
            databaseFactory: databaseFactoryFfi,
            transferAllowed: false,
          );
        }

        final fresh = await open() as SqliteSessionReady;
        final first = native.sentinel;
        expect(first, matches(RegExp(r'^[a-f0-9]{64}$')));
        expect(native.held, false);
        expect(markerWrites, platform == TargetPlatform.iOS ? 1 : 0);
        await fresh.session.close();
        final reopened = await open() as SqliteSessionReady;
        await reopened.session.close();
        expect(native.sentinel, first);
        expect(markerWrites, platform == TargetPlatform.iOS ? 1 : 0);
        final before = await File(native.fresh).readAsBytes();
        native.sentinel = null;
        expect(
          await open(),
          isA<SqliteRestoreHeld>().having(
            (v) => v.reason,
            'missing signal',
            SqliteRestoreReason.installationMissing,
          ),
        );
        expect(await File(native.fresh).readAsBytes(), before);
        expect(native.sentinel, isNull);
        native.sentinel = 'f' * 64;
        expect(
          await open(),
          isA<SqliteRestoreHeld>().having(
            (v) => v.reason,
            'mismatch',
            SqliteRestoreReason.installationMismatch,
          ),
        );
        expect(await File(native.fresh).readAsBytes(), before);
        expect(native.sentinel, 'f' * 64);
        expect(native.held, false);
        native.failExclusion = true;
        await expectLater(open(), throwsA(isA<SqliteStartupException>()));
        expect(await File(native.fresh).readAsBytes(), before);
      },
    );
  }

  test(
    'I5 legacy requires explicit provenance; exact two-location conflict preserves originals',
    () async {
      debugDefaultTargetPlatformOverride = TargetPlatform.android;
      final directory = await Directory.systemTemp.createTemp(
        'lantern-legacy-policy-',
      );
      final native = _NativePolicyFixture(directory.path);
      messenger.setMockMethodCallHandler(
        MobileInstallationPolicy.channel,
        native.call,
      );
      addTearDown(() async {
        messenger.setMockMethodCallHandler(
          MobileInstallationPolicy.channel,
          null,
        );
        debugDefaultTargetPlatformOverride = null;
        await directory.delete(recursive: true);
      });
      final store = await SqliteOfflineStore.open(
        path: native.legacy,
        databaseFactory: databaseFactoryFfi,
      );
      await store.transaction(
        (tx) => tx.effectiveTime('old-fixture', DateTime.now().toUtc()),
      );
      await store.close();
      Future<SqliteSessionStartup> open(bool trusted) async {
        final location = await MobileInstallationPolicy(
          assertLegacyInstallation: trusted ? (_) async => true : null,
        ).resolve(binding: binding, legacyPartition: 'old-fixture');
        expect(
          location.path,
          SqliteOfflineStore.canonicalAutomaticPath(native.legacy),
        );
        final client = LanternClient.connect(
          binding.serviceOrigin,
          allowInsecure: true,
        );
        addTearDown(client.close);
        return SqliteOfflineStore.openSession(
          path: location.path,
          client: client,
          binding: binding,
          installationPolicy: location.policy,
          adoption: location.adoption,
          databaseFactory: databaseFactoryFfi,
          transferAllowed: false,
        );
      }

      final before = await File(native.legacy).readAsBytes();
      expect(await open(false), isA<SqliteRestoreHeld>());
      expect(native.sentinel, isNull);
      expect(await File(native.legacy).readAsBytes(), before);
      final ready = await open(true) as SqliteSessionReady;
      await ready.session.close();
      final repeat = await open(false) as SqliteSessionReady;
      await repeat.session.close();
      await File(native.fresh).writeAsBytes([1]);
      final adopted = await File(native.legacy).readAsBytes();
      await expectLater(
        open(false),
        throwsA(
          isA<SqliteStartupException>().having(
            (v) => v.reason,
            'conflict',
            SqliteStartupFailure.scopeConflict,
          ),
        ),
      );
      expect(await File(native.legacy).readAsBytes(), adopted);
      expect(await File(native.fresh).readAsBytes(), [1]);
    },
  );

  test('I8 no implicit desktop policy', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.windows;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);
    await expectLater(
      MobileInstallationPolicy().resolve(
        binding: binding,
        legacyPartition: 'fixture',
      ),
      throwsA(isA<SqliteStartupException>()),
    );
  });
}

final class _NativePolicyFixture {
  _NativePolicyFixture(String root)
    : fresh = '$root/fresh.db',
      legacy = '$root/legacy.db';
  final String fresh, legacy;
  String? sentinel;
  bool held = false, failExclusion = false;
  Future<Object?> call(MethodCall call) async {
    final args = call.arguments as Map;
    switch (call.method) {
      case 'paths':
        expect(
          args['filename'],
          matches(RegExp(r'^lantern-offline-[0-9a-f]{64}\.db$')),
        );
        return {'fresh': fresh, 'legacy': legacy};
      case 'begin':
        if (failExclusion) {
          throw PlatformException(code: 'installation_policy_failed');
        }
        expect(held, false);
        held = true;
        return {'token': 'fixture-lease', 'installationId': sentinel};
      case 'commit':
        expect(held, true);
        expect(args['token'], 'fixture-lease');
        if (sentinel != null) expect(args['installationId'], sentinel);
        sentinel ??= args['installationId'] as String;
        return null;
      case 'end':
        expect(held, true);
        expect(args['token'], 'fixture-lease');
        held = false;
        return null;
      default:
        fail('Unexpected installation method ${call.method}');
    }
  }
}
