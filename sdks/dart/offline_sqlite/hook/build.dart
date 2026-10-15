// Copyright 2026 anaregdesign. Licensed under the Apache License, Version 2.0.
import 'package:code_assets/code_assets.dart';
import 'package:hooks/hooks.dart';
import 'package:native_toolchain_c/native_toolchain_c.dart';

void main(List<String> args) async {
  await build(args, (input, output) async {
    if (!input.config.buildCodeAssets) return;
    final os = input.config.code.targetOS;
    if (![OS.android, OS.iOS, OS.macOS, OS.linux].contains(os)) {
      throw UnsupportedError('Lantern automatic owner requires POSIX flock');
    }
    await CBuilder.library(
      name: 'lantern_offline_owner',
      assetName: 'src/owner_native.dart',
      sources: ['native/owner.c'],
      std: 'c11',
      defines: {'_DEFAULT_SOURCE': null},
      libraries: os == OS.linux ? ['pthread'] : [],
    ).run(input: input, output: output);
  });
}
