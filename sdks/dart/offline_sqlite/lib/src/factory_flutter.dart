import 'package:sqflite/sqflite.dart' as mobile;
import 'package:sqflite_common/sqlite_api.dart';

/// Uses the mobile operating system SQLite plugin.
DatabaseFactory defaultDatabaseFactory() => mobile.databaseFactory;
