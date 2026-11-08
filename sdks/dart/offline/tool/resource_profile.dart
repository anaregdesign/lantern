/// Only pure latency/RSS envelopes vary. Retention and correctness never do.
enum ResourceProfile {
  full,
  parentContracts;

  String get label => this == full ? 'full' : 'parent-contracts';

  bool envelope(bool condition) => this == parentContracts || condition;

  static ResourceProfile parse(List<String> arguments) {
    if (!arguments.contains('--profile')) return full;
    final index = arguments.indexOf('--profile');
    if (index + 1 >= arguments.length) {
      throw const FormatException('missing resource profile');
    }
    final profile = switch (arguments[index + 1]) {
      'full' => full,
      'parent-contracts' => parentContracts,
      _ => throw const FormatException('unknown resource profile'),
    };
    arguments.removeRange(index, index + 2);
    if (arguments.contains('--profile') ||
        (profile != full && arguments.contains('--diagnostic'))) {
      throw const FormatException('duplicate or diagnostic resource profile');
    }
    return profile;
  }
}
