package com.anaregdesign.lantern_example

import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import java.io.File
import java.io.IOException
import java.io.RandomAccessFile
import java.nio.channels.FileLock
import java.util.UUID
import java.util.zip.ZipFile

class MainActivity : FlutterActivity() {
    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        val installation = OfflineInstallation(this)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "lantern/offline_installation")
            .setMethodCallHandler(installation::handle)
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

// Installation metadata only; never credentials. A nonblocking process lock
// serializes compare/create across account DBs and Flutter isolates. The OS
// releases it on process death; no persistent lease can authorize an overwrite.
private class OfflineInstallation(private val context: android.content.Context) {
    private var file: RandomAccessFile? = null
    private var lock: FileLock? = null
    private var token: String? = null
    private val root get() = File(context.noBackupFilesDir, "lantern-offline-v1")
    private val sentinel get() = File(root, "installation-v1")

    fun handle(call: io.flutter.plugin.common.MethodCall, result: MethodChannel.Result) {
        try {
            when (call.method) {
                "paths", "begin" -> {
                    val name = call.argument<String>("filename") ?: error("filename")
                    require(Regex("lantern-offline-[0-9a-f]{64}\\.db").matches(name))
                    check(root.isDirectory || root.mkdirs())
                    val legacy = context.getDatabasePath(name)
                    if (call.method == "paths") {
                        result.success(mapOf("fresh" to File(root, name).canonicalPath,
                            "legacy" to legacy.canonicalPath))
                        return
                    }
                    check(token == null)
                    val opened = RandomAccessFile(File(root, "installation.lock"), "rw")
                    try {
                        val acquired = opened.channel.tryLock() ?: error("installation busy")
                        file = opened
                        lock = acquired
                        token = UUID.randomUUID().toString()
                        val value = if (sentinel.exists()) {
                            check(sentinel.isFile && sentinel.length() == 64L)
                            sentinel.readText(Charsets.US_ASCII)
                        } else null
                        result.success(mapOf("token" to token, "installationId" to value))
                    } catch (error: Exception) {
                        release()
                        opened.close()
                        throw error
                    }
                }
                "commit" -> {
                    checkToken(call)
                    val value = call.argument<String>("installationId") ?: error("identity")
                    require(Regex("[0-9a-f]{64}").matches(value))
                    if (sentinel.exists()) {
                        check(sentinel.isFile && sentinel.length() == 64L && sentinel.readText(Charsets.US_ASCII) == value)
                    } else {
                        check(sentinel.createNewFile())
                        RandomAccessFile(sentinel, "rw").use {
                            it.write(value.toByteArray(Charsets.US_ASCII))
                            it.fd.sync()
                        }
                    }
                    result.success(null)
                }
                "end" -> {
                    checkToken(call)
                    release()
                    result.success(null)
                }
                else -> result.notImplemented()
            }
        } catch (error: Exception) {
            result.error("installation_policy_failed", "Installation verification failed", null)
        }
    }

    private fun checkToken(call: io.flutter.plugin.common.MethodCall) {
        check(token != null && token == call.argument<String>("token") && lock?.isValid == true)
    }
    private fun release() {
        try { lock?.release() } finally {
            try { file?.close() } finally { file = null; lock = null; token = null }
        }
    }
}
