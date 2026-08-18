package com.piratecash.mwebd

import com.piratecash.mwebdandroid.Daemon
import com.piratecash.mwebdandroid.Mwebdandroid
import com.piratecash.mwebdandroid.StringList
import com.piratecash.mwebdandroid.Utxo
import com.piratecash.mwebdandroid.UtxoListener
import java.io.File
import java.util.concurrent.ExecutionException
import java.util.concurrent.Executors
import java.util.concurrent.Future
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException

internal actual fun createPlatformDaemon(config: MwebdConfig): MwebdDaemon = AndroidMwebdDaemon(config)

internal actual fun platformAddressesMainnet(
    accountKeys: MwebdAccountKeys,
    fromIndex: Int,
    toIndex: Int,
): List<String> {
    return Mwebdandroid.addressesMainnet(
        accountKeys.scanSecretCopy(),
        accountKeys.spendPublicKeyCopy(),
        fromIndex.toLong(),
        toIndex.toLong() + 1,
    ).toListFromCsv()
}

private class AndroidMwebdDaemon(
    private val config: MwebdConfig,
) : MwebdDaemon {
    private val stateLock = Any()
    private val nativeExecutor = Executors.newSingleThreadExecutor { runnable ->
        Thread(runnable, "mwebd-android-native").apply { isDaemon = true }
    }
    private val callbackExecutor = Executors.newSingleThreadExecutor { runnable ->
        Thread(runnable, "mwebd-android-callbacks").apply { isDaemon = true }
    }
    private var state = MwebdDaemonState.New
    private var daemon: Daemon? = null

    override fun start(timeoutMillis: Long): MwebdStatus = synchronized(stateLock) {
        validateTimeout(timeoutMillis)
        if (state == MwebdDaemonState.Running) return@synchronized status(timeoutMillis)
        if (state != MwebdDaemonState.New) throw MwebdStateException("mwebd daemon cannot be started")
        state = MwebdDaemonState.Starting
        try {
            val native = startNativeDaemon(timeoutMillis)
            daemon = native
            state = MwebdDaemonState.Running
            status(timeoutMillis)
        } catch (error: Throwable) {
            state = MwebdDaemonState.Stopping
            daemon?.let { stopAfterFailure(it, error) }
            daemon = null
            state = MwebdDaemonState.Stopped
            nativeExecutor.shutdownNow()
            callbackExecutor.shutdownNow()
            if (error is TimeoutException) {
                throw MwebdTimeoutException("Timed out starting mwebd", error)
            }
            throw error.asMwebdException("Unable to start mwebd")
        }
    }

    override fun stop() = synchronized(stateLock) {
        if (state == MwebdDaemonState.Stopped || state == MwebdDaemonState.Stopping) return@synchronized
        state = MwebdDaemonState.Stopping
        try {
            daemon?.stop()
        } finally {
            nativeExecutor.shutdown()
            callbackExecutor.shutdown()
            daemon = null
            state = MwebdDaemonState.Stopped
        }
    }

    private fun startNativeDaemon(timeoutMillis: Long): Daemon {
        val handoff = NativeDaemonHandoff()
        val future = nativeExecutor.submit<Daemon> {
            createStartedNativeDaemon(handoff)
        }
        return try {
            val native = awaitFuture(future, timeoutMillis) {}
            synchronized(handoff) { handoff.daemon = null }
            native
        } catch (error: Throwable) {
            cancelNativeStartup(handoff, future, error)
            throw error
        }
    }

    private fun createStartedNativeDaemon(handoff: NativeDaemonHandoff): Daemon {
        val native = createNativeDaemon()
        try {
            native.start(0)
            val accepted = synchronized(handoff) {
                if (handoff.cancelled) {
                    false
                } else {
                    handoff.daemon = native
                    true
                }
            }
            if (!accepted) throw InterruptedException("mwebd startup cancelled")
            return native
        } catch (error: Throwable) {
            synchronized(handoff) {
                if (handoff.daemon === native) handoff.daemon = null
            }
            stopAfterFailure(native, error)
            throw error
        }
    }

    private fun cancelNativeStartup(
        handoff: NativeDaemonHandoff,
        future: Future<Daemon>,
        error: Throwable,
    ) {
        val orphan = synchronized(handoff) {
            handoff.cancelled = true
            handoff.daemon.also { handoff.daemon = null }
        }
        future.cancel(true)
        orphan?.let { stopAfterFailure(it, error) }
    }

    private fun dispatchCallback(callback: () -> Unit) {
        try {
            callbackExecutor.execute(callback)
        } catch (_: RejectedExecutionException) {
            // The daemon has already completed shutdown, so late native callbacks are discarded.
        }
    }

    override fun status(timeoutMillis: Long): MwebdStatus {
        validateTimeout(timeoutMillis)
        val native = runningDaemon()
        return try {
            timeoutCall(timeoutMillis) { native.status() }.toCommonStatus()
        } catch (error: TimeoutException) {
            stop()
            throw MwebdTimeoutException("Timed out reading mwebd status", error)
        } catch (error: Throwable) {
            throw error.asMwebdException("Unable to read mwebd status")
        }
    }

    override fun addresses(fromIndex: Int, toIndex: Int): List<String> {
        validateAddressRange(fromIndex, toIndex)
        val keys = config.accountKeys
        return nativeCall("Unable to generate MWEB addresses") {
            runningDaemon().addresses(
                keys.scanSecretCopy(),
                keys.spendPublicKeyCopy(),
                fromIndex.toLong(),
                toIndex.toLong() + 1,
            ).toKotlinList()
        }
    }

    override fun subscribeUtxos(fromHeight: Int, listener: MwebdUtxoListener): MwebdSubscription {
        validateFromHeight(fromHeight)
        val nativeListener = AndroidUtxoListener(listener, ::dispatchCallback)
        val subscription = nativeCall("Unable to subscribe to MWEB UTXOs") {
            runningDaemon().subscribeUtxos(
                fromHeight.toLong(),
                config.accountKeys.scanSecretCopy(),
                nativeListener,
            )
        }
        return MwebdSubscription { subscription.close() }
    }

    override fun spent(outputIds: List<String>): List<String> {
        if (outputIds.isEmpty()) return emptyList()
        validateOutputIds(outputIds)
        return nativeCall("Unable to check spent MWEB outputs") {
            runningDaemon().spent(outputIds.joinToString(",")).toKotlinList()
        }
    }

    override fun create(
        rawTransaction: ByteArray,
        feeRatePerVByte: Int,
        dryRun: Boolean,
    ): MwebdCreateResult {
        val feeRatePerKb = feeRatePerKb(feeRatePerVByte)
        val result = nativeCall("Unable to create MWEB transaction") {
            runningDaemon().create(
                rawTransaction,
                config.accountKeys.scanSecretCopy(),
                config.accountKeys.spendSecretCopy(),
                feeRatePerKb,
                dryRun,
            )
        }
        return MwebdCreateResult(result.rawTx(), result.outputIds().toKotlinList())
    }

    override fun broadcast(rawTransaction: ByteArray): String {
        return nativeCall("Unable to broadcast MWEB transaction") {
            runningDaemon().broadcast(rawTransaction).txId()
        }
    }

    private fun createNativeDaemon(): Daemon {
        File(config.dataDir).mkdirs()
        val checkpoint = config.restoreCheckpoint
        return if (checkpoint.isNullOrEmpty()) {
            Mwebdandroid.newDaemon(
                config.chain.wireName,
                config.dataDir,
                config.peerAddress.orEmpty(),
                config.proxyAddress.orEmpty(),
            )
        } else {
            Mwebdandroid.newDaemonWithRestoreCheckpoint(
                config.chain.wireName,
                config.dataDir,
                config.peerAddress.orEmpty(),
                config.proxyAddress.orEmpty(),
                checkpoint,
            )
        }
    }

    private fun runningDaemon(): Daemon = synchronized(stateLock) {
        if (state != MwebdDaemonState.Running) throw MwebdStateException("mwebd daemon is not running")
        daemon ?: throw MwebdStateException("mwebd daemon is unavailable")
    }

    private fun <T> timeoutCall(timeoutMillis: Long, call: () -> T): T {
        return awaitFuture(nativeExecutor.submit<T>(call), timeoutMillis) {}
    }

    private fun <T> awaitFuture(future: Future<T>, timeoutMillis: Long, onTimeout: () -> Unit): T {
        return try {
            future.get(timeoutMillis, TimeUnit.MILLISECONDS)
        } catch (error: TimeoutException) {
            onTimeout()
            future.cancel(true)
            throw error
        } catch (error: ExecutionException) {
            throw error.cause ?: error
        }
    }
}

private class NativeDaemonHandoff {
    var cancelled = false
    var daemon: Daemon? = null
}

private fun stopAfterFailure(daemon: Daemon, error: Throwable) {
    try {
        daemon.stop()
    } catch (stopError: Throwable) {
        error.addSuppressed(stopError)
    }
}

private class AndroidUtxoListener(
    private val listener: MwebdUtxoListener,
    private val dispatch: (() -> Unit) -> Unit,
) : UtxoListener {
    private var errorDelivered = false
    private var completed = false

    override fun onUtxo(utxo: Utxo) = dispatch {
        if (!completed) {
            listener.onUtxo(
                MwebdUtxo(
                    height = utxo.height().toInt(),
                    value = utxo.value(),
                    address = utxo.address(),
                    outputId = utxo.outputId(),
                    blockTime = utxo.blockTime(),
                ),
            )
        }
    }

    override fun onReplayComplete(height: Long) = dispatch {
        if (!completed) listener.onReplayComplete(height.toInt())
    }

    override fun onError(message: String) = dispatch {
        if (!completed && !errorDelivered) {
            errorDelivered = true
            listener.onError(MwebdTransportException(message))
        }
    }

    override fun onComplete() = dispatch {
        if (!completed) {
            completed = true
            listener.onComplete()
        }
    }
}

private fun com.piratecash.mwebdandroid.Status.toCommonStatus(): MwebdStatus {
    return MwebdStatus(
        blockHeaderHeight = blockHeaderHeight().toInt(),
        mwebHeaderHeight = mwebHeaderHeight().toInt(),
        mwebUtxosHeight = mwebUtxosHeight().toInt(),
        nativeVersion = Mwebdandroid.version(),
        blockTime = blockTime(),
    )
}

private fun StringList.toKotlinList(): List<String> {
    return List(len().toInt()) { index -> get(index.toLong()) }
}

private fun String.toListFromCsv(): List<String> = if (isEmpty()) emptyList() else split(',')

private inline fun <T> nativeCall(message: String, call: () -> T): T {
    return try {
        call()
    } catch (error: MwebdException) {
        throw error
    } catch (error: Throwable) {
        throw MwebdTransportException(message, error)
    }
}
