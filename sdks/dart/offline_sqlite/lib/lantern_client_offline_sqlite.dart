/// Transactional mobile SQLite persistence for Lantern's offline repository.
///
/// The default factory uses the SQLite engine supplied by Android and iOS.
/// Applications own the database location, device protection, credentials, and
/// any encrypted factory. This package never persists credentials or keys.
library;

import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:lantern_client_offline/lantern_client_offline.dart';
import 'package:path/path.dart' as paths;
import 'package:sqflite_common/sqlite_api.dart';

import 'src/factory_stub.dart' if (dart.library.ui) 'src/factory_flutter.dart';

part 'src/errors.dart';
part 'src/schema.dart';
part 'src/store.dart';
part 'src/transaction.dart';
part 'src/validation.dart';
