import 'dart:convert';

import 'package:crypto/crypto.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:lantern_client/lantern_client.dart';

String nativeAccountPartition(Uri endpoint, NativeSession session) => sha256
    .convert(
      utf8.encode(
        jsonEncode([
          endpoint.toString(),
          session.principal.issuer,
          session.principal.subject,
          session.clientProfileId,
        ]),
      ),
    )
    .toString();

/// App-owned secure record. No provider token, pending login proof or Role cache.
final class StoredNativeCredential {
  StoredNativeCredential({
    required this.credential,
    required this.issuer,
    required this.subject,
    required this.profile,
    required this.expiresAt,
  });
  factory StoredNativeCredential.fromSession(
    String credential,
    NativeSession s,
  ) => StoredNativeCredential(
    credential: credential,
    issuer: s.principal.issuer,
    subject: s.principal.subject,
    profile: s.clientProfileId,
    expiresAt: s.expiresAt,
  );
  final String credential, issuer, subject, profile;
  final DateTime expiresAt;
  bool matches(NativeSession s) =>
      issuer == s.principal.issuer &&
      subject == s.principal.subject &&
      profile == s.clientProfileId &&
      expiresAt == s.expiresAt;
  @override
  String toString() => 'StoredNativeCredential(redacted)';
}

abstract interface class NativeCredentialStore {
  Future<StoredNativeCredential?> read();
  Future<void> write(StoredNativeCredential value);
  Future<void> clear();
  Future<List<NativeLogoutReview>> pending(String partition);
  Future<void> savePending(String partition, List<NativeLogoutReview> reviews);
}

/// Device-local Keychain / Android Keystore; Android backup is disabled in the app.
final class SecureNativeCredentialStore implements NativeCredentialStore {
  SecureNativeCredentialStore(Uri endpoint)
    : _key =
          'lantern.native.${sha256.convert(utf8.encode(endpoint.toString()))}';
  final String _key;
  final _storage = const FlutterSecureStorage(
    iOptions: IOSOptions(
      accessibility: KeychainAccessibility.unlocked_this_device,
      synchronizable: false,
    ),
    aOptions: AndroidOptions(resetOnError: false, migrateWithBackup: false),
  );

  @override
  Future<StoredNativeCredential?> read() async {
    final raw = await _storage.read(key: _key);
    if (raw == null) return null;
    try {
      if (raw.length > 8192) throw const FormatException();
      final value = jsonDecode(raw) as Map<String, dynamic>;
      if (value['version'] != 1) throw const FormatException();
      final result = StoredNativeCredential(
        credential: value['credential'] as String,
        issuer: value['issuer'] as String,
        subject: value['subject'] as String,
        profile: value['profile'] as String,
        expiresAt: DateTime.parse(value['expires_at'] as String).toUtc(),
      );
      if (!RegExp(r'^lnu1_[A-Za-z0-9_-]{43}$').hasMatch(result.credential) ||
          !{
            'https://accounts.google.com',
            'https://appleid.apple.com',
          }.contains(result.issuer) ||
          result.subject.isEmpty ||
          result.profile.isEmpty) {
        throw const FormatException();
      }
      return result;
    } on Object {
      throw const FormatException('Invalid stored Lantern session.');
    }
  }

  @override
  Future<void> write(StoredNativeCredential v) => _storage.write(
    key: _key,
    value: jsonEncode({
      'version': 1,
      'credential': v.credential,
      'issuer': v.issuer,
      'subject': v.subject,
      'profile': v.profile,
      'expires_at': v.expiresAt.toUtc().toIso8601String(),
    }),
  );
  @override
  Future<void> clear() => _storage.delete(key: _key);
  @override
  Future<List<NativeLogoutReview>> pending(String partition) async {
    final raw = await _storage.read(key: '$_key.logout.$partition');
    if (raw == null) return [];
    if (raw.length > 512 << 10) {
      throw const FormatException('Invalid logout recovery records.');
    }
    final values = jsonDecode(raw) as List<dynamic>;
    if (values.length > 16) {
      throw const FormatException('Too many logout recovery records.');
    }
    return values
        .map((v) => NativeLogoutReview.fromBytes(base64Decode(v as String)))
        .toList();
  }

  @override
  Future<void> savePending(String partition, List<NativeLogoutReview> reviews) {
    if (reviews.length > 16) {
      throw StateError('Logout recovery capacity reached.');
    }
    final key = '$_key.logout.$partition';
    return reviews.isEmpty
        ? _storage.delete(key: key)
        : _storage.write(
            key: key,
            value: jsonEncode(
              reviews.map((r) => base64Encode(r.toBytes())).toList(),
            ),
          );
  }
}
