package com.piratecash.mwebd

import com.google.protobuf.ByteString
import com.piratecash.mwebd.protocol.AddressRequest
import com.piratecash.mwebd.protocol.BroadcastRequest
import com.piratecash.mwebd.protocol.CreateRequest
import com.piratecash.mwebd.protocol.RpcGrpc
import com.piratecash.mwebd.protocol.SpentRequest
import com.piratecash.mwebd.protocol.StatusRequest
import com.piratecash.mwebd.protocol.Utxo
import com.piratecash.mwebd.protocol.UtxosRequest
import com.piratecash.mwebd.transport.DaemonSidecarConfig
import com.piratecash.mwebd.transport.SidecarLaunchException
import com.piratecash.mwebd.transport.SidecarProcess
import io.grpc.ManagedChannel
import io.grpc.ManagedChannelBuilder
import io.grpc.Metadata
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.stub.ClientCallStreamObserver
import io.grpc.stub.ClientResponseObserver
import io.grpc.stub.MetadataUtils
import java.nio.file.Files
import java.nio.file.Path
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.TimeUnit

private const val AUTH_HEADER = "x-mwebd-token"
private const val STREAM_CLOSE_TIMEOUT_SECONDS = 10L

internal actual fun createPlatformDaemon(config: MwebdConfig): MwebdDaemon = DesktopMwebdDaemon(config)

internal actual fun platformAddressesMainnet(
    accountKeys: MwebdAccountKeys,
    fromIndex: Int,
    toIndex: Int,
): List<String> {
    return try {
        SidecarProcess.addressesMainnet(
            scanSecret = accountKeys.scanSecretCopy(),
            spendPublicKey = accountKeys.spendPublicKeyCopy(),
            fromIndex = fromIndex,
            toIndex = toIndex + 1,
            timeoutMillis = 10_000,
        )
    } catch (error: SidecarLaunchException) {
        if (error.timedOut) {
            throw MwebdTimeoutException("Timed out generating MWEB addresses", error)
        }
        throw MwebdTransportException("Unable to generate MWEB addresses", error)
    }
}

private class DesktopMwebdDaemon(
    private val config: MwebdConfig,
) : MwebdDaemon {
    private val stateLock = Any()
    private val subscriptions = mutableSetOf<DesktopSubscription>()
    private val callbackExecutor = Executors.newSingleThreadExecutor { runnable ->
        Thread(runnable, "mwebd-desktop-callbacks").apply { isDaemon = true }
    }
    private var state = MwebdDaemonState.New
    private var process: SidecarProcess? = null
    private var channel: ManagedChannel? = null
    private var blockingStub: RpcGrpc.RpcBlockingStub? = null
    private var asyncStub: RpcGrpc.RpcStub? = null

    override fun start(timeoutMillis: Long): MwebdStatus = synchronized(stateLock) {
        validateTimeout(timeoutMillis)
        if (state == MwebdDaemonState.Running) return@synchronized status(timeoutMillis)
        if (state != MwebdDaemonState.New) throw MwebdStateException("mwebd daemon cannot be started")
        state = MwebdDaemonState.Starting
        try {
            Files.createDirectories(Path.of(config.dataDir))
            val startedProcess = SidecarProcess.start(config.toSidecarConfig(), timeoutMillis)
            process = startedProcess
            val startedChannel = ManagedChannelBuilder
                .forAddress("127.0.0.1", startedProcess.ready.port)
                .usePlaintext()
                .build()
            channel = startedChannel
            val interceptor = tokenInterceptor(startedProcess.token)
            blockingStub = RpcGrpc.newBlockingStub(startedChannel).withInterceptors(interceptor)
            asyncStub = RpcGrpc.newStub(startedChannel).withInterceptors(interceptor)
            state = MwebdDaemonState.Running
            status(timeoutMillis)
        } catch (error: Throwable) {
            cleanupTransport()
            state = MwebdDaemonState.Stopped
            callbackExecutor.shutdownNow()
            if (error is SidecarLaunchException && error.timedOut) {
                throw MwebdTimeoutException("Timed out starting mwebd sidecar", error)
            }
            throw error.asMwebdException("Unable to start mwebd sidecar")
        }
    }

    override fun stop() = synchronized(stateLock) {
        if (state == MwebdDaemonState.Stopping || state == MwebdDaemonState.Stopped) return@synchronized
        state = MwebdDaemonState.Stopping
        try {
            subscriptions.toList().forEach(DesktopSubscription::close)
        } finally {
            try {
                cleanupTransport()
            } finally {
                subscriptions.clear()
                state = MwebdDaemonState.Stopped
                callbackExecutor.shutdown()
            }
        }
    }

    override fun status(timeoutMillis: Long): MwebdStatus {
        validateTimeout(timeoutMillis)
        val response = rpc("Unable to read mwebd status") {
            runningBlockingStub()
                .withDeadlineAfter(timeoutMillis, TimeUnit.MILLISECONDS)
                .status(StatusRequest.getDefaultInstance())
        }
        val identity = synchronized(stateLock) {
            process?.ready ?: throw MwebdStateException("mwebd sidecar is unavailable")
        }
        return MwebdStatus(
            blockHeaderHeight = response.blockHeaderHeight,
            mwebHeaderHeight = response.mwebHeaderHeight,
            mwebUtxosHeight = response.mwebUtxosHeight,
            nativeVersion = identity.nativeVersion,
            blockTime = Integer.toUnsignedLong(response.blockTime),
        )
    }

    override fun addresses(fromIndex: Int, toIndex: Int): List<String> {
        validateAddressRange(fromIndex, toIndex)
        val keys = config.accountKeys
        val response = rpc("Unable to generate MWEB addresses") {
            runningBlockingStub().addresses(
                AddressRequest.newBuilder()
                    .setFromIndex(fromIndex)
                    .setToIndex(toIndex + 1)
                    .setScanSecret(ByteString.copyFrom(keys.scanSecretCopy()))
                    .setSpendPubkey(ByteString.copyFrom(keys.spendPublicKeyCopy()))
                    .build(),
            )
        }
        return response.addressList
    }

    override fun subscribeUtxos(fromHeight: Int, listener: MwebdUtxoListener): MwebdSubscription {
        validateFromHeight(fromHeight)
        val subscription = DesktopSubscription(
            listener = listener,
            dispatch = ::dispatchCallback,
            terminateTransport = ::terminateTransport,
        ) { completed -> synchronized(stateLock) { subscriptions.remove(completed) } }
        val request = UtxosRequest.newBuilder()
            .setFromHeight(fromHeight)
            .setScanSecret(ByteString.copyFrom(config.accountKeys.scanSecretCopy()))
            .build()
        try {
            synchronized(stateLock) {
                if (state != MwebdDaemonState.Running) throw MwebdStateException("mwebd daemon is not running")
                val stub = asyncStub ?: throw MwebdStateException("mwebd transport is unavailable")
                subscriptions.add(subscription)
                stub.utxos(request, subscription)
            }
        } catch (error: Throwable) {
            subscription.failStart()
            throw error.asMwebdException("Unable to subscribe to MWEB UTXOs")
        }
        return subscription
    }

    override fun spent(outputIds: List<String>): List<String> {
        if (outputIds.isEmpty()) return emptyList()
        validateOutputIds(outputIds)
        val response = rpc("Unable to check spent MWEB outputs") {
            runningBlockingStub().spent(
                SpentRequest.newBuilder().addAllOutputId(outputIds).build(),
            )
        }
        return response.outputIdList
    }

    override fun create(
        rawTransaction: ByteArray,
        feeRatePerVByte: Int,
        dryRun: Boolean,
    ): MwebdCreateResult {
        val response = rpc("Unable to create MWEB transaction") {
            runningBlockingStub().create(
                CreateRequest.newBuilder()
                    .setRawTx(ByteString.copyFrom(rawTransaction))
                    .setScanSecret(ByteString.copyFrom(config.accountKeys.scanSecretCopy()))
                    .setSpendSecret(ByteString.copyFrom(config.accountKeys.spendSecretCopy()))
                    .setFeeRatePerKb(feeRatePerKb(feeRatePerVByte))
                    .setDryRun(dryRun)
                    .build(),
            )
        }
        return MwebdCreateResult(response.rawTx.toByteArray(), response.outputIdList)
    }

    override fun broadcast(rawTransaction: ByteArray): String {
        return rpc("Unable to broadcast MWEB transaction") {
            runningBlockingStub().broadcast(
                BroadcastRequest.newBuilder()
                    .setRawTx(ByteString.copyFrom(rawTransaction))
                    .build(),
            ).txid
        }
    }

    private fun runningBlockingStub(): RpcGrpc.RpcBlockingStub = synchronized(stateLock) {
        if (state != MwebdDaemonState.Running) throw MwebdStateException("mwebd daemon is not running")
        blockingStub ?: throw MwebdStateException("mwebd transport is unavailable")
    }

    private fun cleanupTransport() {
        val resources = synchronized(stateLock) {
            val current = channel to process
            channel = null
            process = null
            blockingStub = null
            asyncStub = null
            current
        }
        var interrupted = false
        try {
            resources.first?.shutdownNow()
            try {
                resources.first?.awaitTermination(5, TimeUnit.SECONDS)
            } catch (_: InterruptedException) {
                interrupted = true
            }
        } finally {
            try {
                resources.second?.close()
            } finally {
                if (interrupted) Thread.currentThread().interrupt()
            }
        }
    }

    private fun terminateTransport() {
        synchronized(stateLock) {
            channel?.shutdownNow()
            process?.terminate()
        }
    }

    private fun dispatchCallback(callback: () -> Unit) {
        try {
            callbackExecutor.execute(callback)
        } catch (_: RejectedExecutionException) {
            // The daemon has already completed shutdown, so late transport callbacks are discarded.
        }
    }
}

private class DesktopSubscription(
    private val listener: MwebdUtxoListener,
    private val dispatch: (() -> Unit) -> Unit,
    private val terminateTransport: () -> Unit,
    private val onFinished: (DesktopSubscription) -> Unit,
) : ClientResponseObserver<UtxosRequest, Utxo>, MwebdSubscription {
    private val callbackLock = Any()
    private val finished = CountDownLatch(1)
    private var request: ClientCallStreamObserver<UtxosRequest>? = null
    private var closing = false
    private var completed = false

    override fun beforeStart(requestStream: ClientCallStreamObserver<UtxosRequest>) {
        val cancel = synchronized(callbackLock) {
            request = requestStream
            closing
        }
        if (cancel) requestStream.cancel("MWEB subscription closed", null)
    }

    override fun onNext(value: Utxo) {
        val callback = synchronized(callbackLock) {
            if (completed) return
            if (value.isReplayComplete()) {
                { listener.onReplayComplete(value.replayCompleteHeight) }
            } else {
                { listener.onUtxo(value.toCommonUtxo()) }
            }
        }
        dispatch(callback)
    }

    override fun onError(error: Throwable) {
        val reportError = synchronized(callbackLock) {
            !closing && Status.fromThrowable(error).code != Status.Code.CANCELLED
        }
        finish(if (reportError) error else null)
    }

    override fun onCompleted() {
        finish(null)
    }

    override fun close() {
        val activeRequest = synchronized(callbackLock) {
            if (completed) return
            closing = true
            request
        }
        activeRequest?.cancel("MWEB subscription closed", null)
        val closed = try {
            finished.await(STREAM_CLOSE_TIMEOUT_SECONDS, TimeUnit.SECONDS)
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
            false
        }
        if (!closed) {
            terminateTransport()
            finish(null)
        }
    }

    fun failStart() {
        val changed = synchronized(callbackLock) {
            if (completed) return
            completed = true
            true
        }
        if (changed) {
            finished.countDown()
            onFinished(this)
        }
    }

    private fun finish(error: Throwable?) {
        val callback = synchronized(callbackLock) {
            if (completed) return
            completed = true
            {
                if (error != null) listener.onError(MwebdTransportException("MWEB UTXO stream failed", error))
                listener.onComplete()
            }
        }
        dispatch(callback)
        finished.countDown()
        onFinished(this)
    }
}

internal fun Utxo.isReplayComplete(): Boolean {
    return replayComplete &&
        height == 0 &&
        value == 0L &&
        address.isEmpty() &&
        outputId.isEmpty() &&
        blockTime == 0
}

private fun Utxo.toCommonUtxo(): MwebdUtxo {
    return MwebdUtxo(
        height = height,
        value = value,
        address = address,
        outputId = outputId,
        blockTime = Integer.toUnsignedLong(blockTime),
    )
}

private fun MwebdConfig.toSidecarConfig(): DaemonSidecarConfig {
    return DaemonSidecarConfig(
        chain = chain.wireName,
        dataDir = dataDir,
        peerAddress = peerAddress.orEmpty(),
        proxyAddress = proxyAddress.orEmpty(),
        restoreCheckpoint = restoreCheckpoint.orEmpty(),
    )
}

private fun tokenInterceptor(token: String) = MetadataUtils.newAttachHeadersInterceptor(
    Metadata().apply {
        put(Metadata.Key.of(AUTH_HEADER, Metadata.ASCII_STRING_MARSHALLER), token)
    },
)

private inline fun <T> rpc(message: String, call: () -> T): T {
    return try {
        call()
    } catch (error: MwebdException) {
        throw error
    } catch (error: StatusRuntimeException) {
        if (error.status.code == Status.Code.DEADLINE_EXCEEDED) {
            throw MwebdTimeoutException(message, error)
        }
        throw MwebdTransportException(message, error)
    } catch (error: Throwable) {
        throw MwebdTransportException(message, error)
    }
}
