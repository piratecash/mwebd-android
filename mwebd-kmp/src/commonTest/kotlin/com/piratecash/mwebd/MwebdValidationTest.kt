package com.piratecash.mwebd

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class MwebdValidationTest {
    @Test
    fun validateAddressRange_inclusiveUpperBoundIntMax_rejected() {
        assertFailsWith<MwebdValidationException> {
            validateAddressRange(0, Int.MAX_VALUE)
        }
    }

    @Test
    fun validateAddressRange_validInclusiveRange_accepted() {
        validateAddressRange(7, 11)
    }

    @Test
    fun feeRatePerKb_validRate_convertsExactlyOnce() {
        assertEquals(25_000L, feeRatePerKb(25))
    }

    @Test
    fun feeRatePerKb_negativeRate_rejected() {
        assertFailsWith<MwebdValidationException> {
            feeRatePerKb(-1)
        }
    }

    @Test
    fun validateOutputIds_wrongLengthOrNonHex_rejected() {
        assertFailsWith<MwebdValidationException> {
            validateOutputIds(listOf("01", "z".repeat(64)))
        }
    }

    @Test
    fun validateOutputIds_validHash_accepted() {
        validateOutputIds(listOf("aB".repeat(32)))
    }

    @Test
    fun create_wrongCompressedPublicKeyLength_rejectedBeforePlatformCall() {
        val keys = MwebdAccountKeys(ByteArray(32), ByteArray(32), ByteArray(32))

        assertFailsWith<MwebdValidationException> {
            Mwebd.create(MwebdConfig(MwebdChain.Mainnet, "test-data", keys))
        }
    }

    @Test
    fun createResult_inputArrayMutated_keepsSnapshot() {
        val rawTransaction = byteArrayOf(1, 2, 3)
        val result = MwebdCreateResult(rawTransaction, listOf("output"))

        rawTransaction[0] = 9

        assertEquals(1, result.rawTransaction[0])
    }

    @Test
    fun configToString_proxyWithCredentials_redactsEntireProxyAddress() {
        val config = MwebdConfig(
            chain = MwebdChain.Mainnet,
            dataDir = "test-data",
            accountKeys = MwebdAccountKeys(ByteArray(32), ByteArray(32), ByteArray(33)),
            proxyAddress = "socks5://alice:secret@127.0.0.1:9050",
        )

        val description = config.toString()

        assertTrue(description.contains("proxyAddress=<redacted>"))
        assertFalse(description.contains("alice"))
        assertFalse(description.contains("secret"))
    }
}
