package com.piratecash.mwebd.transport

import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.TimeoutException
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class SidecarPlatformTest {
    @Test
    fun detect_supportedTargets_mapsExactArtifacts() {
        assertEquals("linux-x64", SidecarPlatform.detect("Linux", "amd64").directory)
        assertEquals("windows-x64", SidecarPlatform.detect("Windows 11", "x86_64").directory)
        assertEquals("macos-arm64", SidecarPlatform.detect("Mac OS X", "aarch64").directory)
    }

    @Test
    fun detect_unplannedDesktopTarget_rejected() {
        assertFailsWith<UnsupportedOperationException> {
            SidecarPlatform.detect("Mac OS X", "x86_64")
        }
    }

    @Test
    fun executable_concurrentCallers_returnsSameBinary() {
        val start = CountDownLatch(1)
        val executor = Executors.newFixedThreadPool(8)
        try {
            val futures = List(16) {
                executor.submit {
                    start.await()
                    SidecarBinary.executable()
                }
            }
            start.countDown()
            val paths = futures.map { it.get(30, TimeUnit.SECONDS) }

            assertEquals(1, paths.toSet().size)
        } finally {
            executor.shutdownNow()
        }
    }

    @Test
    fun timedOut_nestedLaunchFailure_preservesReason() {
        val timeout = SidecarLaunchException("timeout", TimeoutException())
        val wrapped = SidecarLaunchException("launch failed", timeout)

        assertTrue(wrapped.timedOut)
    }
}
