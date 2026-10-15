import Flutter
import UIKit
import Darwin

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
    let installation = OfflineInstallation()
    FlutterMethodChannel(
      name: "lantern/offline_installation",
      binaryMessenger: engineBridge.applicationRegistrar.messenger()
    ).setMethodCallHandler(installation.handle)
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

// Coordinates the existing FlutterSecureStorage ThisDeviceOnly marker with a
// separate excluded sentinel. No credential, DB payload or account is retained.
private final class OfflineInstallation {
  private var descriptor: Int32 = -1
  private var token: String?
  private let fm = FileManager.default
  private var sentinel: URL?
  private enum Failure: Error { case policy }

  private func exclude(_ url: URL) throws {
    var target = url
    var values = URLResourceValues()
    values.isExcludedFromBackup = true
    try target.setResourceValues(values)
    guard try target.resourceValues(forKeys: [.isExcludedFromBackupKey]).isExcludedFromBackup == true
    else { throw Failure.policy }
  }

  func handle(_ call: FlutterMethodCall, result: @escaping FlutterResult) {
    do {
      let args = call.arguments as? [String: Any] ?? [:]
      switch call.method {
      case "paths", "begin":
        guard let name = args["filename"] as? String,
          name.range(of: "^lantern-offline-[0-9a-f]{64}\\.db$", options: .regularExpression) != nil
        else { throw Failure.policy }
        let support = try fm.url(for: .applicationSupportDirectory, in: .userDomainMask,
                                 appropriateFor: nil, create: true)
        let root = support.appendingPathComponent("lantern-offline-v1", isDirectory: true)
        try fm.createDirectory(at: root, withIntermediateDirectories: true)
        try exclude(root)
        // sqflite's maintained iOS getDatabasesPath is Documents. Keep the exact
        // old DB there; excluding the directory also covers future companions.
        let legacyRoot = try fm.url(for: .documentDirectory, in: .userDomainMask,
                                    appropriateFor: nil, create: true)
        try exclude(legacyRoot)
        let legacy = legacyRoot.appendingPathComponent(name)
        let fresh = root.appendingPathComponent(name)
        for db in [legacy, fresh] {
          for suffix in ["", "-wal", "-shm", "-journal"] {
            let item = URL(fileURLWithPath: db.path + suffix)
            if fm.fileExists(atPath: item.path) { try exclude(item) }
          }
        }
        if call.method == "paths" {
          result(["fresh": fresh.resolvingSymlinksInPath().path,
                  "legacy": legacy.resolvingSymlinksInPath().path])
          return
        }
        guard descriptor == -1 else { throw Failure.policy }
        let fd = Darwin.open(root.appendingPathComponent("installation.lock").path,
                             O_RDWR | O_CREAT | O_NOFOLLOW, S_IRUSR | S_IWUSR)
        guard fd >= 0 else { throw Failure.policy }
        guard flock(fd, LOCK_EX | LOCK_NB) == 0 else {
          Darwin.close(fd); throw Failure.policy
        }
        descriptor = fd
        token = UUID().uuidString
        sentinel = root.appendingPathComponent("installation-v1")
        do {
          var output: [String: Any] = ["token": token!]
          if fm.fileExists(atPath: sentinel!.path) {
            let attrs = try fm.attributesOfItem(atPath: sentinel!.path)
            guard attrs[.type] as? FileAttributeType == .typeRegular,
              attrs[.size] as? Int == 64,
              let value = String(data: try Data(contentsOf: sentinel!), encoding: .ascii)
            else { throw Failure.policy }
            output["installationId"] = value
          }
          result(output)
        } catch { try? release(); throw error }
      case "commit":
        try checkToken(args)
        guard let value = args["installationId"] as? String,
          value.range(of: "^[0-9a-f]{64}$", options: .regularExpression) != nil,
          let destination = sentinel
        else { throw Failure.policy }
        if fm.fileExists(atPath: destination.path) {
          guard try Data(contentsOf: destination) == Data(value.utf8) else { throw Failure.policy }
        } else {
          // Exclusive creation never replaces a sentinel after an uncertain
          // write; a partial marker is a held failure on the next startup.
          let fd = Darwin.open(destination.path, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW,
                               S_IRUSR | S_IWUSR)
          guard fd >= 0 else { throw Failure.policy }
          let bytes = Array(value.utf8)
          let count = bytes.withUnsafeBytes { Darwin.write(fd, $0.baseAddress, $0.count) }
          let synced = fsync(fd)
          let closed = Darwin.close(fd)
          guard count == bytes.count && synced == 0 && closed == 0 else { throw Failure.policy }
        }
        try exclude(destination)
        result(nil)
      case "end":
        try checkToken(args)
        try release()
        result(nil)
      default: result(FlutterMethodNotImplemented)
      }
    } catch {
      result(FlutterError(code: "installation_policy_failed",
                          message: "Installation verification failed", details: nil))
    }
  }
  private func checkToken(_ args: [String: Any]) throws {
    guard descriptor >= 0, token != nil, token == args["token"] as? String
    else { throw Failure.policy }
  }
  private func release() throws {
    let fd = descriptor
    descriptor = -1; token = nil; sentinel = nil
    guard fd >= 0 else { throw Failure.policy }
    let unlocked = flock(fd, LOCK_UN)
    let closed = Darwin.close(fd)
    guard unlocked == 0 && closed == 0 else { throw Failure.policy }
  }
}
