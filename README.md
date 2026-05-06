# mwebd-android

Android AAR packaging for [`ltcmweb/mwebd`](https://github.com/ltcmweb/mwebd).

The artifact is intended to be consumed through JitPack:

```kotlin
repositories {
    maven("https://jitpack.io")
}

dependencies {
    implementation("com.github.piratecash:mwebd-android:<commit_version>")
}
```

## What This Contains

- A small Go wrapper around `github.com/ltcmweb/mwebd`.
- A pinned upstream dependency: `github.com/ltcmweb/mwebd v0.1.19`.
- A Gradle `maven-publish` setup for JitPack.
- A `gomobile bind` build that produces an Android AAR with native libraries for:
  - `armeabi-v7a`
  - `arm64-v8a`
  - `x86_64`
- A 16 KB ELF alignment check for generated native libraries.

Consumers of the published AAR do not need Go, gomobile, or Android NDK in their
own build environment.

## Local Build

Prerequisites:

- JDK 17
- Android SDK and NDK
- Go 1.24+

Build and publish to Maven local:

```bash
./gradlew publishToMavenLocal
```

Build only the AAR:

```bash
./gradlew buildMwebdAar
```

Verify generated native libraries are compatible with Android 16 KB page-size
devices:

```bash
./gradlew verifyElfAlignment
```

## Kotlin Usage Sketch

`gomobile bind` generates Java/Kotlin-callable bindings under the
`com.piratecash.mwebdandroid` package.

```kotlin
val daemon = Mwebdandroid.newDaemon(
    "mainnet",
    filesDir.resolve("mwebd").absolutePath,
    "",
    ""
)

daemon.start(0)
val status = daemon.status()
daemon.stop()
```

The final `litecoinkit` integration should wrap these generated bindings behind
its own Kotlin interface, so app code never depends on gomobile-generated API
directly.
