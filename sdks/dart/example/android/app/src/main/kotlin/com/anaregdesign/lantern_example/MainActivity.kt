package com.anaregdesign.lantern_example

import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import java.io.File
import java.io.IOException
import java.util.zip.ZipFile

class MainActivity : FlutterActivity() {
    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        MethodChannel(
            flutterEngine.dartExecutor.binaryMessenger,
            "lantern/physical_receipt_attestation",
        ).setMethodCallHandler { call, result ->
            if (call.method != "installedBinary") {
                result.notImplemented()
                return@setMethodCallHandler
            }

            val source = applicationInfo.sourceDir
            if (source.isNullOrBlank() || !applicationInfo.splitSourceDirs.isNullOrEmpty()) {
                result.error("installed_binary_unavailable", "A single installed APK is required", null)
                return@setMethodCallHandler
            }

            try {
                val apk = File(source)
                if (!apk.isFile || !apk.canRead()) {
                    result.error("installed_binary_unavailable", "The installed APK is unreadable", null)
                    return@setMethodCallHandler
                }
                val containsDartAot = ZipFile(apk).use { zip ->
                    zip.entries().asSequence().any { entry ->
                        !entry.isDirectory &&
                            entry.name.startsWith("lib/") &&
                            entry.name.endsWith("/libapp.so") &&
                            entry.size > 0
                    }
                }
                if (!containsDartAot) {
                    result.error("installed_binary_unavailable", "The installed APK has no Dart AOT", null)
                    return@setMethodCallHandler
                }
                result.success(
                    mapOf(
                        "platform" to "android",
                        "packageId" to packageName,
                        "path" to apk.canonicalPath,
                    ),
                )
            } catch (error: IOException) {
                result.error("installed_binary_unavailable", "The installed APK cannot be inspected", null)
            } catch (error: SecurityException) {
                result.error("installed_binary_unavailable", "The installed APK cannot be read", null)
            }
        }
    }
}
