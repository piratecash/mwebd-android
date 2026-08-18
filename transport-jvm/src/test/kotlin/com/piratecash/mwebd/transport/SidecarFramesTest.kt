package com.piratecash.mwebd.transport

import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.io.DataOutputStream
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class SidecarFramesTest {
    @Test
    fun writeInit_daemonFrame_containsProtocolIdentity() {
        val output = ByteArrayOutputStream()

        SidecarFrames.writeInit(
            output,
            SidecarInit(mode = SidecarMode.Daemon, token = "token", chain = "regtest"),
        )

        assertTrue(output.toByteArray().startsWith("MWEBINI1".encodeToByteArray()))
    }

    @Test
    fun readReady_validFrame_decodesIdentity() {
        val output = ByteArrayOutputStream()
        DataOutputStream(output).apply {
            write("MWEBRDY1".encodeToByteArray())
            writeInt(1)
            writeField("ltcmweb/mwebd v0.1.19, mwebd-kmp 1.0.0 (abc)")
            writeInt(1234)
        }

        val ready = SidecarFrames.readReady(ByteArrayInputStream(output.toByteArray()))

        assertEquals("ltcmweb/mwebd v0.1.19, mwebd-kmp 1.0.0 (abc)", ready.nativeVersion)
        assertEquals(1234, ready.port)
    }

    @Test
    fun readReady_wrongMagic_rejected() {
        assertFailsWith<SidecarProtocolException> {
            SidecarFrames.readReady(ByteArrayInputStream(ByteArray(8)))
        }
    }

    private fun DataOutputStream.writeField(value: String) {
        val bytes = value.encodeToByteArray()
        writeInt(bytes.size)
        write(bytes)
    }

    private fun ByteArray.startsWith(prefix: ByteArray): Boolean {
        return size >= prefix.size && prefix.indices.all { index -> this[index] == prefix[index] }
    }
}
