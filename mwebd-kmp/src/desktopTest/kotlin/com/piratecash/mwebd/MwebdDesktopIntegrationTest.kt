package com.piratecash.mwebd

import com.piratecash.mwebd.protocol.Utxo
import java.nio.file.Files
import java.nio.file.Path
import java.util.Comparator
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class MwebdDesktopIntegrationTest {
    @Test
    fun addressesMainnet_validAccountKeys_generatesRequestedAddresses() {
        val addresses = Mwebd.addressesMainnet(
            accountKeys = testAccountKeys(),
            fromIndex = 0,
            toIndex = 1,
        )

        assertEquals(2, addresses.size)
        assertTrue(addresses.all(String::isNotBlank))
        assertEquals(addresses.size, addresses.distinct().size)
    }

    @Test
    fun replayComplete_heightZero_usesExplicitMarker() {
        val replayComplete = Utxo.newBuilder()
            .setReplayComplete(true)
            .setReplayCompleteHeight(0)
            .build()

        assertTrue(replayComplete.isReplayComplete())
        assertFalse(Utxo.getDefaultInstance().isReplayComplete())
    }

    @Test
    fun subscription_replayCompleteAtHeightZero_canCloseFromCallback() {
        val dataDirectory = Files.createTempDirectory("mwebd-kmp-stream-test-")
        val daemon = Mwebd.create(MwebdConfig(MwebdChain.Regtest, dataDirectory.toString(), testAccountKeys()))
        val subscription = AtomicReference<MwebdSubscription>()
        val subscriptionReady = CountDownLatch(1)
        val listener = ClosingReplayListener(subscription, subscriptionReady)
        try {
            daemon.start(30_000)
            subscription.set(daemon.subscribeUtxos(0, listener))
            subscriptionReady.countDown()

            assertTrue(listener.completed.await(10, TimeUnit.SECONDS))
            assertEquals(0, listener.replayHeight.get())
            assertEquals(null, listener.error.get())
        } finally {
            subscriptionReady.countDown()
            daemon.stop()
            deleteRecursively(dataDirectory)
        }
    }

    @Test
    fun stop_interruptedThread_stillStopsDaemon() {
        val dataDirectory = Files.createTempDirectory("mwebd-kmp-interrupted-stop-")
        val daemon = Mwebd.create(MwebdConfig(MwebdChain.Regtest, dataDirectory.toString(), testAccountKeys()))
        try {
            daemon.start(30_000)
            daemon.subscribeUtxos(0, NoOpUtxoListener)
            Thread.currentThread().interrupt()

            daemon.stop()

            assertTrue(Thread.interrupted())
            assertFailsWith<MwebdStateException> { daemon.status() }
        } finally {
            Thread.interrupted()
            daemon.stop()
            deleteRecursively(dataDirectory)
        }
    }
}

private object NoOpUtxoListener : MwebdUtxoListener {
    override fun onUtxo(utxo: MwebdUtxo) = Unit
    override fun onReplayComplete(height: Int) = Unit
    override fun onError(error: Throwable) = Unit
    override fun onComplete() = Unit
}

private class ClosingReplayListener(
    private val subscription: AtomicReference<MwebdSubscription>,
    private val subscriptionReady: CountDownLatch,
) : MwebdUtxoListener {
    val replayHeight = AtomicReference<Int?>()
    val error = AtomicReference<Throwable?>()
    val completed = CountDownLatch(1)

    override fun onUtxo(utxo: MwebdUtxo) = Unit

    override fun onReplayComplete(height: Int) {
        replayHeight.set(height)
        if (subscriptionReady.await(5, TimeUnit.SECONDS)) subscription.get()?.close()
    }

    override fun onError(error: Throwable) {
        this.error.set(error)
    }

    override fun onComplete() {
        completed.countDown()
    }
}

private fun testAccountKeys(): MwebdAccountKeys {
    return MwebdAccountKeys(
        scanSecret = "8111873a68eea9226633679fdbb7c722febb2faf9cb466f774abb5687bb507bf".hexToByteArray(),
        spendSecret = "eb38f3bc766af919cbb291647d1c2353acfd062b0190b69c8578a0e8f58f72a2".hexToByteArray(),
        spendPublicKey = "027ea462511404c1eb774f0e9c4fda2d2930e40a69d27e7b48c11201e2830d0d0c".hexToByteArray(),
    )
}

private fun deleteRecursively(directory: Path) {
    Files.walk(directory).use { paths ->
        paths.sorted(Comparator.reverseOrder()).forEach(Files::deleteIfExists)
    }
}
