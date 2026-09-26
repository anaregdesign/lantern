import Flutter
import UIKit

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate {
  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  func didInitializeImplicitFlutterEngine(_ engineBridge: FlutterImplicitEngineBridge) {
    GeneratedPluginRegistrant.register(with: engineBridge.pluginRegistry)
    let channel = FlutterMethodChannel(
      name: "lantern/physical_receipt_attestation",
      binaryMessenger: engineBridge.applicationRegistrar.messenger()
    )
    channel.setMethodCallHandler { call, result in
      guard call.method == "installedBinary" else {
        result(FlutterMethodNotImplemented)
        return
      }
#if targetEnvironment(simulator)
      result(FlutterError(
        code: "physical_device_required",
        message: "Receipt attestation requires an installed physical iOS app",
        details: nil
      ))
#else
      guard let executable = Bundle.main.privateFrameworksURL?
        .appendingPathComponent("App.framework/App"),
        let packageId = Bundle.main.bundleIdentifier,
        FileManager.default.isReadableFile(atPath: executable.path)
      else {
        result(FlutterError(
          code: "installed_binary_unavailable",
          message: "The installed Dart AOT executable is unreadable",
          details: nil
        ))
        return
      }
      result([
        "platform": "ios",
        "packageId": packageId,
        "path": executable.path
      ])
#endif
    }
  }
}
