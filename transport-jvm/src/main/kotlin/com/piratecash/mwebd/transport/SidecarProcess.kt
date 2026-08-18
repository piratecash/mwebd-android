package com.piratecash.mwebd.transport

import java.io.BufferedReader
import java.io.InputStreamReader
import java.nio.file.Path
import java.security.SecureRandom
import java.util.Base64
import java.util.concurrent.Callable
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException
import java.util.concurrent.atomic.AtomicBoolean

private const val SHUTDOWN_TIMEOUT_SECONDS = 10L
private val CREDENTIAL_URL = Regex("(?i)([a-z][a-z0-9+.-]*://)[^@\\s]+@")

data class DaemonSidecarConfig(
    val chain: String,
    val dataDir: String,
    val peerAddress: String,
    val proxyAddress: String,
    val restoreCheckpoint: String,
)

class SidecarProcess private constructor(
    private val process: Process,
    private val stderr: SidecarStderr,
    val token: String,
    val ready: SidecarReady,
) : AutoCloseable {
    private val closed = AtomicBoolean(false)

    override fun close() {
        if (!closed.compareAndSet(false, true)) return
        var interrupted = false
        try {
            process.outputStream.close()
            if (!process.waitFor(SHUTDOWN_TIMEOUT_SECONDS, TimeUnit.SECONDS)) {
                process.destroy()
            }
            if (process.isAlive && !process.waitFor(2, TimeUnit.SECONDS)) {
                process.destroyForcibly()
            }
        } catch (_: InterruptedException) {
            interrupted = true
        } finally {
            if (process.isAlive) {
                process.destroyForcibly()
                try {
                    process.waitFor(2, TimeUnit.SECONDS)
                } catch (_: InterruptedException) {
                    interrupted = true
                }
            }
            stderr.close()
            if (interrupted) Thread.currentThread().interrupt()
        }
    }

    fun terminate() {
        if (closed.compareAndSet(false, true)) {
            process.destroyForcibly()
            stderr.close()
        }
    }

    companion object {
        fun start(config: DaemonSidecarConfig, timeoutMillis: Long): SidecarProcess {
            val token = randomToken()
            val process = startProcess(SidecarBinary.executable())
            val stderr = SidecarStderr(process)
            try {
                SidecarFrames.writeInit(
                    process.outputStream,
                    SidecarInit(
                        mode = SidecarMode.Daemon,
                        token = token,
                        chain = config.chain,
                        dataDir = config.dataDir,
                        peerAddress = config.peerAddress,
                        proxyAddress = config.proxyAddress,
                        restoreCheckpoint = config.restoreCheckpoint,
                    ),
                )
                val ready = readWithTimeout(timeoutMillis) { SidecarFrames.readReady(process.inputStream) }
                return SidecarProcess(process, stderr, token, ready)
            } catch (error: Throwable) {
                process.destroyForcibly()
                stderr.close()
                throw SidecarLaunchException(stderr.failureMessage(), error)
            }
        }

        fun addressesMainnet(
            scanSecret: ByteArray,
            spendPublicKey: ByteArray,
            fromIndex: Int,
            toIndex: Int,
            timeoutMillis: Long,
        ): List<String> {
            val process = startProcess(SidecarBinary.executable())
            val stderr = SidecarStderr(process)
            return try {
                SidecarFrames.writeInit(
                    process.outputStream,
                    SidecarInit(
                        mode = SidecarMode.Addresses,
                        scanSecret = scanSecret,
                        spendPublicKey = spendPublicKey,
                        fromIndex = fromIndex,
                        toIndex = toIndex,
                    ),
                )
                process.outputStream.close()
                val addresses = readWithTimeout(timeoutMillis) {
                    SidecarFrames.readAddresses(process.inputStream)
                }
                if (!process.waitFor(timeoutMillis, TimeUnit.MILLISECONDS) || process.exitValue() != 0) {
                    throw SidecarLaunchException(stderr.failureMessage())
                }
                addresses
            } catch (error: Throwable) {
                process.destroyForcibly()
                throw SidecarLaunchException(stderr.failureMessage(), error)
            } finally {
                stderr.close()
            }
        }

        private fun startProcess(executable: Path): Process {
            return ProcessBuilder(executable.toString())
                .redirectErrorStream(false)
                .start()
        }

        private fun randomToken(): String {
            val bytes = ByteArray(32)
            SecureRandom().nextBytes(bytes)
            return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)
        }

        private fun <T> readWithTimeout(timeoutMillis: Long, read: () -> T): T {
            val executor = Executors.newSingleThreadExecutor { runnable ->
                Thread(runnable, "mwebd-sidecar-startup").apply { isDaemon = true }
            }
            return try {
                executor.submit(Callable(read)).get(timeoutMillis, TimeUnit.MILLISECONDS)
            } catch (error: TimeoutException) {
                throw SidecarLaunchException("Timed out waiting for the mwebd sidecar", error)
            } finally {
                executor.shutdownNow()
            }
        }
    }
}

private class SidecarStderr(process: Process) : AutoCloseable {
    private val lines = ArrayDeque<String>()
    private val thread = Thread({ drain(process) }, "mwebd-sidecar-stderr").apply {
        isDaemon = true
        start()
    }

    @Synchronized
    fun failureMessage(): String {
        return lines.lastOrNull()?.let { "mwebd sidecar failed: $it" } ?: "mwebd sidecar failed"
    }

    override fun close() {
        thread.interrupt()
    }

    private fun drain(process: Process) {
        BufferedReader(InputStreamReader(process.errorStream)).useLines { sequence ->
            sequence.forEach(::record)
        }
    }

    @Synchronized
    private fun record(line: String) {
        if (lines.size == 20) lines.removeFirst()
        lines.addLast(line.replace(CREDENTIAL_URL, "$1***@"))
    }
}

class SidecarLaunchException(message: String, cause: Throwable? = null) : Exception(message, cause) {
    val timedOut: Boolean = cause is TimeoutException || (cause as? SidecarLaunchException)?.timedOut == true
}
