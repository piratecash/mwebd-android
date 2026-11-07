# mwebd-kmp

Kotlin Multiplatform bindings for [`ltcmweb/mwebd`](https://github.com/ltcmweb/mwebd).

The library exposes one Kotlin API from `commonMain` and provides platform runtimes for:

- Android API 24+ (`armeabi-v7a`, `arm64-v8a`, and `x86_64`) through gomobile.
- Desktop JVM 21 on Ubuntu 22.04+/glibc 2.35+ x64, Windows 10 22H2+ x64, and macOS 13+ ARM64
  through an authenticated native sidecar.

iOS, 32-bit desktop systems, Linux ARM64, macOS x64, and Android x86 are not release targets.

## Artifacts

Releases use exact numeric SemVer tags such as `1.0.0` and are published through JitPack:

```kotlin
repositories {
    maven("https://jitpack.io")
}

dependencies {
    implementation("com.github.piratecash.mwebd-android:mwebd-kmp:<version>")
}
```

`mwebd-kmp` selects the Android or Desktop implementation and brings its runtime transitively.
The legacy `com.github.piratecash:mwebd-android:<version>` Android AAR remains published for
existing consumers during migration.

## Modules

- `mwebd-kmp` — common API plus Android and Desktop implementations.
- `native-android` — gomobile AAR built from the pinned Go sources.
- `transport-jvm` — gRPC client, sidecar lifecycle, and packaged native Desktop executables.

Both platform implementations use the same code in `go/mwebdandroid`, including the pinned and
locally patched `github.com/ltcmweb/mwebd v0.1.19` and `neutrino` sources.

Desktop starts the sidecar on loopback with an ephemeral port and a per-process random token.
Configuration and proxy credentials are sent over stdin, not command-line arguments or environment
variables. Release binaries are downloaded by JitPack only after their version, commit, size, and
SHA-256 values match the GitHub Release manifest.

## Usage

```kotlin
val keys = MwebdAccountKeys(
    scanSecret = scanSecret,
    spendPublicKey = spendPublicKey,
    spendSecret = spendSecret,
)
val daemon = Mwebd.create(
    MwebdConfig(
        chain = MwebdChain.Mainnet,
        dataDir = dataDirectory,
        accountKeys = keys,
    ),
)

val status = daemon.start()
val addresses = daemon.addresses(fromIndex = 0, toIndex = 9)
daemon.stop()
```

`MwebdDaemon` is terminal after `stop()`: create a new instance to start again. Close every UTXO
subscription when its owner is disposed.

## Local verification

Prerequisites are JDK 21, Go 1.24+, Android SDK 35, and Android NDK 27.0.12077973.

```bash
./gradlew :mwebd-kmp:desktopTest
./gradlew :mwebd-kmp:testReleaseUnitTest :native-android:verifyElfAlignment
(cd go/mwebdandroid && go test ./...)
(cd go/mwebdandroid/third_party/mwebd && go test ./...)
```

A local Desktop build embeds only the current host executable. Tagged CI builds and checks all
three Desktop executables, publishes them as GitHub Release assets, and JitPack verifies and embeds
that complete set in `transport-jvm`.

Consumers do not need Go, gomobile, or the Android NDK.
