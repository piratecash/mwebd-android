package com.piratecash.mwebd.transport

import java.io.FileOutputStream
import java.nio.channels.FileChannel
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.FileAlreadyExistsException
import java.nio.file.Files
import java.nio.file.LinkOption
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.nio.file.StandardOpenOption
import java.nio.file.attribute.AclEntry
import java.nio.file.attribute.AclEntryPermission
import java.nio.file.attribute.AclEntryType
import java.nio.file.attribute.AclFileAttributeView
import java.nio.file.attribute.PosixFilePermission
import java.security.MessageDigest
import java.util.EnumSet
import java.util.Locale

private const val HASH_ALGORITHM = "SHA-256"

internal data class SidecarPlatform(
    val directory: String,
    val fileName: String,
) {
    companion object {
        fun current(): SidecarPlatform {
            return detect(
                System.getProperty("os.name").orEmpty(),
                System.getProperty("os.arch").orEmpty(),
            )
        }

        internal fun detect(osName: String, architecture: String): SidecarPlatform {
            val os = osName.lowercase(Locale.ROOT)
            val arch = architecture.lowercase(Locale.ROOT)
            return when {
                os.contains("mac") && arch in setOf("aarch64", "arm64") ->
                    SidecarPlatform("macos-arm64", "mwebd-sidecar-macos-arm64")
                os.contains("linux") && arch in setOf("amd64", "x86_64") ->
                    SidecarPlatform("linux-x64", "mwebd-sidecar-linux-x64")
                os.contains("windows") && arch in setOf("amd64", "x86_64") ->
                    SidecarPlatform("windows-x64", "mwebd-sidecar-windows-x64.exe")
                else -> throw UnsupportedOperationException("Unsupported desktop target: $osName/$architecture")
            }
        }
    }
}

object SidecarBinary {
    private val installLock = Any()

    fun executable(): Path = synchronized(installLock) {
        val platform = SidecarPlatform.current()
        val resourcePath = "mwebd/${platform.directory}/${platform.fileName}"
        val expectedHash = resourceHash(resourcePath)
        val targetDirectory = cacheRoot().resolve(version()).resolve(expectedHash)
        createSafeDirectories(targetDirectory)
        val target = targetDirectory.resolve(platform.fileName)
        val lock = target.resolveSibling("${platform.fileName}.lock")
        rejectSymlink(lock)
        FileChannel.open(
            lock,
            StandardOpenOption.CREATE,
            StandardOpenOption.WRITE,
            LinkOption.NOFOLLOW_LINKS,
        ).use { channel ->
            channel.lock().use {
                installIfNeeded(resourcePath, expectedHash, target)
            }
        }
        verifyHash(target, expectedHash)
        target
    }

    fun stageForPackaging(destinationDirectory: Path): Path {
        createSafeDirectories(destinationDirectory)
        val source = executable()
        val destination = destinationDirectory.resolve(source.fileName.toString())
        Files.copy(source, destination, StandardCopyOption.REPLACE_EXISTING, LinkOption.NOFOLLOW_LINKS)
        applyOwnerOnlyPermissions(destination)
        return destination
    }

    private fun installIfNeeded(resourcePath: String, expectedHash: String, target: Path) {
        rejectSymlink(target)
        if (Files.exists(target, LinkOption.NOFOLLOW_LINKS)) {
            verifyHash(target, expectedHash)
            return
        }
        val temporary = Files.createTempFile(target.parent, ".mwebd-", ".tmp")
        try {
            copyResource(resourcePath, temporary)
            applyOwnerOnlyPermissions(temporary)
            verifyHash(temporary, expectedHash)
            atomicMove(temporary, target)
        } finally {
            Files.deleteIfExists(temporary)
        }
    }

    private fun copyResource(resourcePath: String, target: Path) {
        resourceStream(resourcePath).use { input ->
            FileOutputStream(target.toFile()).use { output ->
                input.copyTo(output)
                output.fd.sync()
            }
        }
    }

    private fun resourceHash(resourcePath: String): String {
        val digest = MessageDigest.getInstance(HASH_ALGORITHM)
        resourceStream(resourcePath).use { input ->
            val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
            while (true) {
                val read = input.read(buffer)
                if (read < 0) break
                digest.update(buffer, 0, read)
            }
        }
        return digest.digest().toHex()
    }

    private fun verifyHash(path: Path, expectedHash: String) {
        rejectSymlink(path)
        val actual = Files.newInputStream(path, LinkOption.NOFOLLOW_LINKS).use { input ->
            val digest = MessageDigest.getInstance(HASH_ALGORITHM)
            val buffer = ByteArray(DEFAULT_BUFFER_SIZE)
            while (true) {
                val read = input.read(buffer)
                if (read < 0) break
                digest.update(buffer, 0, read)
            }
            digest.digest().toHex()
        }
        if (actual != expectedHash) {
            throw SecurityException("mwebd sidecar checksum mismatch")
        }
    }

    private fun resourceStream(resourcePath: String) =
        SidecarBinary::class.java.classLoader.getResourceAsStream(resourcePath)
            ?: throw IllegalStateException("Missing sidecar resource: $resourcePath")

    private fun cacheRoot(): Path {
        val home = Path.of(System.getProperty("user.home"))
        val os = System.getProperty("os.name").orEmpty().lowercase(Locale.ROOT)
        return when {
            os.contains("mac") -> home.resolve("Library/Caches/PirateCash/mwebd")
            os.contains("windows") -> {
                val localAppData = System.getenv("LOCALAPPDATA")?.takeIf(String::isNotBlank)
                Path.of(localAppData ?: home.resolve("AppData/Local").toString()).resolve("PirateCash/mwebd")
            }
            else -> {
                val xdgCache = System.getenv("XDG_CACHE_HOME")?.takeIf(String::isNotBlank)
                Path.of(xdgCache ?: home.resolve(".cache").toString()).resolve("piratecash/mwebd")
            }
        }
    }

    private fun createSafeDirectories(path: Path) {
        val absolute = path.toAbsolutePath().normalize()
        var current = absolute.root
        for (part in absolute) {
            current = current.resolve(part)
            if (Files.exists(current, LinkOption.NOFOLLOW_LINKS)) {
                rejectSymlink(current)
            } else {
                try {
                    Files.createDirectory(current)
                } catch (error: FileAlreadyExistsException) {
                    rejectSymlink(current)
                    if (!Files.isDirectory(current, LinkOption.NOFOLLOW_LINKS)) throw error
                }
            }
        }
    }

    private fun rejectSymlink(path: Path) {
        if (Files.isSymbolicLink(path)) {
            throw SecurityException("Symbolic links are not allowed in the mwebd runtime path")
        }
    }

    private fun applyOwnerOnlyPermissions(path: Path) {
        val posix = try {
            Files.getPosixFilePermissions(path)
        } catch (_: UnsupportedOperationException) {
            null
        }
        if (posix != null) {
            Files.setPosixFilePermissions(
                path,
                setOf(
                    PosixFilePermission.OWNER_READ,
                    PosixFilePermission.OWNER_WRITE,
                    PosixFilePermission.OWNER_EXECUTE,
                ),
            )
            return
        }
        val view = Files.getFileAttributeView(path, AclFileAttributeView::class.java) ?: return
        val ownerEntry = AclEntry.newBuilder()
            .setType(AclEntryType.ALLOW)
            .setPrincipal(view.owner)
            .setPermissions(EnumSet.allOf(AclEntryPermission::class.java))
            .build()
        view.acl = listOf(ownerEntry)
    }

    private fun atomicMove(source: Path, target: Path) {
        try {
            Files.move(source, target, StandardCopyOption.ATOMIC_MOVE)
        } catch (_: AtomicMoveNotSupportedException) {
            Files.move(source, target)
        }
    }

    private fun version(): String {
        return SidecarBinary::class.java.`package`.implementationVersion
            ?.takeIf(String::isNotBlank)
            ?.replace(Regex("[^A-Za-z0-9._-]"), "_")
            ?: "local"
    }

    private fun ByteArray.toHex(): String = joinToString(separator = "") { byte -> "%02x".format(byte) }
}
