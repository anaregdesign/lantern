import 'dart:convert';

import 'package:lantern_client/lantern_client.dart';
import 'package:test/test.dart';

import 'native_auth_test.dart' as fixtures;
import 'native_registration_test.dart' show registrationReview;

void main() {
  test('review recovery bytes reject malformed or unbound variants', () {
    final valid = registrationReview();
    List<int> encode() => utf8.encode(
      jsonEncode({
        'version': 1,
        'endpoint': fixtures.endpoint.toString(),
        'review': base64Encode(valid.writeToBuffer()),
      }),
    );
    final review = NativeRegistrationReview.fromBytes(encode());
    expect(review.toBytes(), encode());
    expect(review.toString(), isNot(contains('exact-subject')));
    for (final malformed in <List<int>>[
      [],
      List.filled(32769, 0),
      utf8.encode('{}'),
    ]) {
      expect(
        () => NativeRegistrationReview.fromBytes(malformed),
        throwsA(isA<LanternInvalidArgumentException>()),
      );
    }
    valid.userId = '00000000000000000000000000000000';
    expect(
      () => NativeRegistrationReview.fromBytes(encode()),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
    valid.userId = '0123456789abcdef0123456789abcdef';
    valid.actor.machineName = 'mixed-identity';
    expect(
      () => NativeRegistrationReview.fromBytes(encode()),
      throwsA(isA<LanternInvalidArgumentException>()),
    );
  });
}
