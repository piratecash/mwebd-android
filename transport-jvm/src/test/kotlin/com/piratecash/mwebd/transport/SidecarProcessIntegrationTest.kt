package com.piratecash.mwebd.transport

import com.piratecash.mwebd.protocol.RpcGrpc
import com.piratecash.mwebd.protocol.StatusRequest
import io.grpc.ManagedChannelBuilder
import io.grpc.Metadata
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.stub.MetadataUtils
import java.nio.file.Files
import java.nio.file.Path
import java.util.Comparator
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class SidecarProcessIntegrationTest {
    @Test
    fun statusRpc_missingTokenRejected_exactTokenAccepted() {
        withSidecar { sidecar ->
            assertTrue(sidecar.ready.nativeVersion.startsWith("ltcmweb/mwebd v0.1.19, mwebd-kmp "))
            val channel = ManagedChannelBuilder.forAddress("127.0.0.1", sidecar.ready.port)
                .usePlaintext()
                .build()
            try {
                val unauthenticated = assertFailsWith<StatusRuntimeException> {
                    RpcGrpc.newBlockingStub(channel).status(StatusRequest.getDefaultInstance())
                }
                assertEquals(Status.Code.UNAUTHENTICATED, unauthenticated.status.code)

                RpcGrpc.newBlockingStub(channel)
                    .withInterceptors(tokenInterceptor(sidecar.token))
                    .status(StatusRequest.getDefaultInstance())
            } finally {
                channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS)
            }
        }
    }

    @Test
    fun close_interruptedThread_terminatesProcessAndPreservesInterrupt() {
        val dataDirectory = Files.createTempDirectory("mwebd-sidecar-interrupted-close-")
        val sidecar = SidecarProcess.start(
            DaemonSidecarConfig("regtest", dataDirectory.toString(), "", "", ""),
            timeoutMillis = 30_000,
        )
        val channel = ManagedChannelBuilder.forAddress("127.0.0.1", sidecar.ready.port)
            .usePlaintext()
            .build()
        try {
            Thread.currentThread().interrupt()

            sidecar.close()

            assertTrue(Thread.interrupted())
            assertFailsWith<StatusRuntimeException> {
                RpcGrpc.newBlockingStub(channel)
                    .withInterceptors(tokenInterceptor(sidecar.token))
                    .withDeadlineAfter(5, TimeUnit.SECONDS)
                    .status(StatusRequest.getDefaultInstance())
            }
        } finally {
            Thread.interrupted()
            channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS)
            sidecar.terminate()
            deleteRecursively(dataDirectory)
        }
    }

    private fun withSidecar(block: (SidecarProcess) -> Unit) {
        val dataDirectory = Files.createTempDirectory("mwebd-sidecar-test-")
        try {
            SidecarProcess.start(
                DaemonSidecarConfig("regtest", dataDirectory.toString(), "", "", ""),
                timeoutMillis = 30_000,
            ).use(block)
        } finally {
            deleteRecursively(dataDirectory)
        }
    }

    private fun tokenInterceptor(token: String) = MetadataUtils.newAttachHeadersInterceptor(
        Metadata().apply {
            put(Metadata.Key.of("x-mwebd-token", Metadata.ASCII_STRING_MARSHALLER), token)
        },
    )

    private fun deleteRecursively(directory: Path) {
        Files.walk(directory).use { paths ->
            paths.sorted(Comparator.reverseOrder()).forEach(Files::deleteIfExists)
        }
    }
}
