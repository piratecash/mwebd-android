package com.piratecash.mwebd.transport

import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.InputStream
import java.io.OutputStream

private const val PROTOCOL_VERSION = 1
private const val MAX_FIELD_SIZE = 64 * 1024 * 1024
private val INIT_MAGIC = "MWEBINI1".encodeToByteArray()
private val READY_MAGIC = "MWEBRDY1".encodeToByteArray()
private val ADDRESSES_MAGIC = "MWEBADR1".encodeToByteArray()

internal enum class SidecarMode(val code: Int) {
    Daemon(0),
    Addresses(1),
}

internal data class SidecarInit(
    val mode: SidecarMode,
    val token: String = "",
    val chain: String = "",
    val dataDir: String = "",
    val peerAddress: String = "",
    val proxyAddress: String = "",
    val restoreCheckpoint: String = "",
    val scanSecret: ByteArray = byteArrayOf(),
    val spendPublicKey: ByteArray = byteArrayOf(),
    val fromIndex: Int = 0,
    val toIndex: Int = 0,
)

data class SidecarReady(
    val protocolVersion: Int,
    val nativeVersion: String,
    val port: Int,
)

internal object SidecarFrames {
    fun writeInit(output: OutputStream, init: SidecarInit) {
        DataOutputStream(output).apply {
            write(INIT_MAGIC)
            writeInt(PROTOCOL_VERSION)
            writeByte(init.mode.code)
            writeField(init.token.encodeToByteArray())
            writeField(init.chain.encodeToByteArray())
            writeField(init.dataDir.encodeToByteArray())
            writeField(init.peerAddress.encodeToByteArray())
            writeField(init.proxyAddress.encodeToByteArray())
            writeField(init.restoreCheckpoint.encodeToByteArray())
            writeField(init.scanSecret)
            writeField(init.spendPublicKey)
            writeInt(init.fromIndex)
            writeInt(init.toIndex)
            flush()
        }
    }

    fun readReady(input: InputStream): SidecarReady {
        val data = DataInputStream(input)
        data.requireMagic(READY_MAGIC)
        val protocolVersion = data.readInt()
        if (protocolVersion != PROTOCOL_VERSION) {
            throw SidecarProtocolException("Unsupported sidecar protocol: $protocolVersion")
        }
        val nativeVersion = data.readField().decodeToString()
        val port = data.readInt()
        if (nativeVersion.isBlank() || port !in 1..65535) {
            throw SidecarProtocolException("Invalid sidecar startup identity")
        }
        return SidecarReady(protocolVersion, nativeVersion, port)
    }

    fun readAddresses(input: InputStream): List<String> {
        val data = DataInputStream(input)
        data.requireMagic(ADDRESSES_MAGIC)
        val count = data.readInt()
        if (count < 0 || count > 1_000_000) {
            throw SidecarProtocolException("Invalid address count: $count")
        }
        return List(count) { data.readField().decodeToString() }
    }

    private fun DataOutputStream.writeField(value: ByteArray) {
        if (value.size > MAX_FIELD_SIZE) {
            throw SidecarProtocolException("Sidecar field is too large: ${value.size}")
        }
        writeInt(value.size)
        write(value)
    }

    private fun DataInputStream.readField(): ByteArray {
        val size = readInt()
        if (size < 0 || size > MAX_FIELD_SIZE) {
            throw SidecarProtocolException("Invalid sidecar field size: $size")
        }
        return ByteArray(size).also(::readFully)
    }

    private fun DataInputStream.requireMagic(expected: ByteArray) {
        val actual = ByteArray(expected.size).also(::readFully)
        if (!actual.contentEquals(expected)) {
            throw SidecarProtocolException("Invalid sidecar frame magic")
        }
    }
}

class SidecarProtocolException(message: String, cause: Throwable? = null) : Exception(message, cause)
