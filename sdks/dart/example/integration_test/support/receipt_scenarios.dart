const receiptMatrixTarget =
    'integration_test/physical_receipt_matrix_test.dart';

const receiptCommonScenarios = <String>{
  'receipt_authenticated_trusted_tls',
  'receipt_untrusted_tls_rejected',
  'receipt_conditional_put_exact',
  'receipt_vertex_delete_exact',
  'receipt_edge_delete_exact',
  'receipt_contribution_add_after_delete',
  'receipt_nonfinite_derived_float32',
  'receipt_committed_response_loss',
  'receipt_real_sigkill_sqlite_reopen',
  'receipt_relaunch_status_first_no_resend',
  'receipt_radio_foreground_recovery',
};

const receiptAndroidScenario = 'android_doze_like_pause';
const receiptIosScenario = 'ios_local_network_privacy_denial_retry';

Set<String> requiredReceiptScenarios(String platform) => switch (platform) {
  'android' => {...receiptCommonScenarios, receiptAndroidScenario},
  'ios' => {...receiptCommonScenarios, receiptIosScenario},
  _ => throw ArgumentError.value(
    platform,
    'platform',
    'Physical device required',
  ),
};
