package com.piratecash.mwebd

private const val DEFAULT_TIMEOUT_MILLIS = 10_000L
private const val FEE_RATE_KB_MULTIPLIER = 1_000L
private const val SECRET_KEY_SIZE = 32
private const val COMPRESSED_PUBLIC_KEY_SIZE = 33
private val OUTPUT_ID = Regex("[0-9a-fA-F]{64}")

enum class MwebdChain(internal val wireName: String) {
    Mainnet("mainnet"),
    Testnet("testnet"),
    Regtest("regtest"),
}

class MwebdAccountKeys(
    scanSecret: ByteArray,
    spendSecret: ByteArray,
    spendPublicKey: ByteArray,
) {
    private val scanSecretBytes = scanSecret.copyOf()
    private val spendPublicKeyBytes = spendPublicKey.copyOf()
    private val spendSecretBytes = spendSecret.copyOf()

    val scanSecret: ByteArray get() = scanSecretBytes.copyOf()
    val spendPublicKey: ByteArray get() = spendPublicKeyBytes.copyOf()
    val spendSecret: ByteArray get() = spendSecretBytes.copyOf()

    internal fun scanSecretCopy(): ByteArray = scanSecretBytes.copyOf()
    internal fun spendPublicKeyCopy(): ByteArray = spendPublicKeyBytes.copyOf()
    internal fun spendSecretCopy(): ByteArray = spendSecretBytes.copyOf()
}

data class MwebdConfig(
    val chain: MwebdChain,
    val dataDir: String,
    val accountKeys: MwebdAccountKeys,
    val peerAddress: String? = null,
    val proxyAddress: String? = null,
    val restoreCheckpoint: String? = null,
) {
    override fun toString(): String {
        return "MwebdConfig(" +
            "chain=$chain, " +
            "dataDir=$dataDir, " +
            "accountKeys=$accountKeys, " +
            "peerAddress=$peerAddress, " +
            "proxyAddress=${proxyAddress?.let { "<redacted>" }}, " +
            "restoreCheckpoint=$restoreCheckpoint)"
    }
}

data class MwebdStatus(
    val blockHeaderHeight: Int,
    val mwebHeaderHeight: Int,
    val mwebUtxosHeight: Int,
    val nativeVersion: String,
    val blockTime: Long = 0,
)

data class MwebdUtxo(
    val height: Int,
    val value: Long,
    val address: String,
    val outputId: String,
    val blockTime: Long,
)

class MwebdCreateResult(
    rawTransaction: ByteArray,
    val outputIds: List<String>,
) {
    private val rawTransactionBytes = rawTransaction.copyOf()

    val rawTransaction: ByteArray get() = rawTransactionBytes.copyOf()

    override fun equals(other: Any?): Boolean {
        return other is MwebdCreateResult &&
            rawTransactionBytes.contentEquals(other.rawTransactionBytes) &&
            outputIds == other.outputIds
    }

    override fun hashCode(): Int {
        return 31 * rawTransactionBytes.contentHashCode() + outputIds.hashCode()
    }
}

interface MwebdUtxoListener {
    fun onUtxo(utxo: MwebdUtxo)
    fun onReplayComplete(height: Int)
    fun onError(error: Throwable)
    fun onComplete()
}

fun interface MwebdSubscription {
    fun close()
}

interface MwebdDaemon {
    fun start(timeoutMillis: Long = DEFAULT_TIMEOUT_MILLIS): MwebdStatus
    fun stop()
    fun status(timeoutMillis: Long = DEFAULT_TIMEOUT_MILLIS): MwebdStatus
    fun addresses(fromIndex: Int, toIndex: Int): List<String>
    fun subscribeUtxos(fromHeight: Int, listener: MwebdUtxoListener): MwebdSubscription
    fun spent(outputIds: List<String>): List<String>
    fun create(rawTransaction: ByteArray, feeRatePerVByte: Int, dryRun: Boolean): MwebdCreateResult
    fun broadcast(rawTransaction: ByteArray): String
}

fun interface MwebdDaemonFactory {
    fun create(config: MwebdConfig): MwebdDaemon
}

object Mwebd : MwebdDaemonFactory {
    override fun create(config: MwebdConfig): MwebdDaemon {
        validateConfig(config)
        return createPlatformDaemon(config)
    }

    fun addressesMainnet(
        accountKeys: MwebdAccountKeys,
        fromIndex: Int,
        toIndex: Int,
    ): List<String> {
        validateAddressRange(fromIndex, toIndex)
        validateAccountKeys(accountKeys)
        return platformAddressesMainnet(accountKeys, fromIndex, toIndex)
    }
}

open class MwebdException(message: String, cause: Throwable? = null) : RuntimeException(message, cause)

class MwebdStateException(message: String) : MwebdException(message)

class MwebdValidationException(message: String) : MwebdException(message)

class MwebdTimeoutException(message: String, cause: Throwable? = null) : MwebdException(message, cause)

class MwebdTransportException(message: String, cause: Throwable? = null) : MwebdException(message, cause)

internal expect fun createPlatformDaemon(config: MwebdConfig): MwebdDaemon

internal expect fun platformAddressesMainnet(
    accountKeys: MwebdAccountKeys,
    fromIndex: Int,
    toIndex: Int,
): List<String>

internal fun validateTimeout(timeoutMillis: Long) {
    if (timeoutMillis <= 0) {
        throw MwebdValidationException("timeoutMillis must be positive")
    }
}

internal fun validateAddressRange(fromIndex: Int, toIndex: Int) {
    if (fromIndex < 0 || toIndex < fromIndex || toIndex == Int.MAX_VALUE) {
        throw MwebdValidationException("invalid inclusive address index range")
    }
}

internal fun validateFromHeight(fromHeight: Int) {
    if (fromHeight < 0) {
        throw MwebdValidationException("fromHeight must not be negative")
    }
}

internal fun feeRatePerKb(feeRatePerVByte: Int): Long {
    if (feeRatePerVByte < 0) {
        throw MwebdValidationException("feeRatePerVByte must not be negative")
    }
    return feeRatePerVByte.toLong() * FEE_RATE_KB_MULTIPLIER
}

internal fun validateOutputIds(outputIds: List<String>) {
    if (outputIds.any { !OUTPUT_ID.matches(it) }) {
        throw MwebdValidationException("output IDs must be 32-byte hexadecimal values")
    }
}

internal fun Throwable.asMwebdException(message: String): MwebdException {
    return this as? MwebdException ?: MwebdTransportException(message, this)
}

internal enum class MwebdDaemonState {
    New,
    Starting,
    Running,
    Stopping,
    Stopped,
}

private fun validateConfig(config: MwebdConfig) {
    if (config.dataDir.isBlank()) {
        throw MwebdValidationException("dataDir must not be blank")
    }
    validateAccountKeys(config.accountKeys)
}

private fun validateAccountKeys(accountKeys: MwebdAccountKeys) {
    if (accountKeys.scanSecretCopy().size != SECRET_KEY_SIZE) {
        throw MwebdValidationException("scanSecret must contain 32 bytes")
    }
    if (accountKeys.spendPublicKeyCopy().size != COMPRESSED_PUBLIC_KEY_SIZE) {
        throw MwebdValidationException("spendPublicKey must contain 33 bytes")
    }
    if (accountKeys.spendSecretCopy().size != SECRET_KEY_SIZE) {
        throw MwebdValidationException("spendSecret must contain 32 bytes")
    }
}
