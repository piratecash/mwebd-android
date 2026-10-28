package com.piratecash.mwebd.transport

import java.io.IOException
import java.nio.file.Files
import java.nio.file.Path
import org.junit.Assume
import org.junit.Rule
import org.junit.rules.TemporaryFolder
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class SidecarBinaryTest {
    @get:Rule
    val temporaryFolder = TemporaryFolder()

    private val root: Path get() = temporaryFolder.root.toPath()

    @Test
    fun createSafeDirectories_trustedBaseBehindSymlink_createsOwnedPathUnderRealBase() {
        val real = Files.createDirectory(root.resolve("real"))
        val link = createSymlinkOrSkip(root.resolve("link"), real)

        val result = SidecarBinary.createSafeDirectories(link, Path.of("a", "b"))

        assertEquals(real.toRealPath().resolve("a").resolve("b"), result)
        assertTrue(Files.isDirectory(result))
    }

    @Test
    fun createSafeDirectories_symlinkInsideOwnedPath_throwsSecurityException() {
        val base = Files.createDirectory(root.resolve("base"))
        val elsewhere = Files.createDirectory(root.resolve("elsewhere"))
        createSymlinkOrSkip(base.resolve("a"), elsewhere)

        assertFailsWith<SecurityException> {
            SidecarBinary.createSafeDirectories(base, Path.of("a", "b"))
        }
        assertFalse(Files.exists(elsewhere.resolve("b")))
    }

    @Test
    fun createSafeDirectories_missingTrustedBase_createsIt() {
        val base = root.resolve("x").resolve("y")

        val result = SidecarBinary.createSafeDirectories(base, Path.of("a"))

        assertEquals(base.toRealPath().resolve("a"), result)
        assertTrue(Files.isDirectory(result))
    }

    private fun createSymlinkOrSkip(link: Path, target: Path): Path =
        try {
            Files.createSymbolicLink(link, target)
        } catch (error: UnsupportedOperationException) {
            Assume.assumeNoException(error)
            link
        } catch (error: IOException) {
            Assume.assumeNoException(error)
            link
        }
}
