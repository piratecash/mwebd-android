plugins {
    base
    `maven-publish`
}

group = "com.github.piratecash"
version = providers.environmentVariable("JITPACK_VERSION")
    .orElse(providers.environmentVariable("VERSION"))
    .orElse(providers.environmentVariable("VERSION_NAME"))
    .orElse(provider { gitShortSha() })
    .get()

val mwebdAar = layout.buildDirectory.file("outputs/aar/mwebd-android.aar")
val goPackageDir = layout.projectDirectory.dir("go/mwebdandroid")
val xMobileVersion = "v0.0.0-20250210185054-b38b8813d607"
val android16KbLdFlags = "-linkmode=external -extldflags=-Wl,-z,max-page-size=16384,-z,common-page-size=16384"

tasks.register<Exec>("installGomobileTools") {
    workingDir = goPackageDir.asFile
    commandLine(
        "bash",
        "-lc",
        """
            set -euo pipefail
            go install golang.org/x/mobile/cmd/gomobile@${xMobileVersion}
            go install golang.org/x/mobile/cmd/gobind@${xMobileVersion}
        """.trimIndent()
    )
}

tasks.register<Exec>("initGomobile") {
    dependsOn("installGomobileTools")
    workingDir = goPackageDir.asFile
    commandLine(
        "bash",
        "-lc",
        """
            set -euo pipefail
            export PATH="$(go env GOPATH)/bin:${'$'}PATH"
            "$(go env GOPATH)/bin/gomobile" init
        """.trimIndent()
    )
}

val buildMwebdAar by tasks.registering(Exec::class) {
    dependsOn("initGomobile")

    workingDir = goPackageDir.asFile
    inputs.files(fileTree(goPackageDir))
    inputs.property("android16KbLdFlags", android16KbLdFlags)
    inputs.property("xMobileVersion", xMobileVersion)
    outputs.file(mwebdAar)

    doFirst {
        mwebdAar.get().asFile.parentFile.mkdirs()
    }

    commandLine(
        "bash",
        "-lc",
        """
            set -euo pipefail
            export PATH="$(go env GOPATH)/bin:${'$'}PATH"
            "$(go env GOPATH)/bin/gomobile" bind \
              -target=android/arm,android/arm64,android/amd64 \
              -androidapi=24 \
              -javapkg=com.piratecash \
              -ldflags='${android16KbLdFlags}' \
              -o "${mwebdAar.get().asFile.absolutePath}" \
              .
        """.trimIndent()
    )
}

val verifyElfAlignment by tasks.registering(Exec::class) {
    dependsOn(buildMwebdAar)

    inputs.file(mwebdAar)
    val verifyDir = layout.buildDirectory.dir("tmp/verifyElfAlignment")
    outputs.dir(verifyDir)

    commandLine(
        "bash",
        "-lc",
        """
            set -euo pipefail

            work="${verifyDir.get().asFile.absolutePath}"
            rm -rf "${'$'}work"
            mkdir -p "${'$'}work"
            cd "${'$'}work"
            jar xf "${mwebdAar.get().asFile.absolutePath}"

            readelf=""
            for candidate in \
              "${'$'}{ANDROID_NDK_HOME:-}/toolchains/llvm/prebuilt"/*/bin/llvm-readelf \
              "${'$'}{ANDROID_SDK_ROOT:-${'$'}{ANDROID_HOME:-}}"/ndk/*/toolchains/llvm/prebuilt/*/bin/llvm-readelf \
              "${'$'}{ANDROID_HOME:-}"/ndk/*/toolchains/llvm/prebuilt/*/bin/llvm-readelf
            do
              if [[ -x "${'$'}candidate" ]]; then
                readelf="${'$'}candidate"
                break
              fi
            done

            if [[ -z "${'$'}readelf" ]]; then
              echo "llvm-readelf not found; set ANDROID_NDK_HOME or ANDROID_SDK_ROOT"
              exit 1
            fi

            failed=0
            found=0
            while IFS= read -r so; do
              found=1
              while IFS= read -r align; do
                if (( align < 0x4000 )); then
                  echo "ELF LOAD alignment ${'$'}align is below 16 KB: ${'$'}so"
                  failed=1
                fi
              done < <("${'$'}readelf" -lW "${'$'}so" | awk '/LOAD/ { print ${'$'}NF }')
            done < <(find jni -name "*.so" | sort)

            if (( found == 0 )); then
              echo "No native libraries found in AAR"
              failed=1
            fi

            exit "${'$'}failed"
        """.trimIndent()
    )
}

tasks.assemble {
    dependsOn(verifyElfAlignment)
}

tasks.check {
    dependsOn(verifyElfAlignment)
}

tasks.named("publishToMavenLocal") {
    dependsOn(verifyElfAlignment)
}

publishing {
    publications {
        create<MavenPublication>("release") {
            artifact(mwebdAar) {
                builtBy(buildMwebdAar)
                extension = "aar"
            }

            groupId = "com.github.piratecash"
            artifactId = "mwebd-android"
            version = project.version.toString()

            pom {
                name.set("mwebd-android")
                description.set("Android AAR packaging for ltcmweb/mwebd")
                url.set("https://github.com/piratecash/mwebd-android")
                licenses {
                    license {
                        name.set("MIT License")
                        url.set("https://opensource.org/licenses/MIT")
                    }
                }
                scm {
                    connection.set("scm:git:https://github.com/piratecash/mwebd-android.git")
                    developerConnection.set("scm:git:ssh://git@github.com/piratecash/mwebd-android.git")
                    url.set("https://github.com/piratecash/mwebd-android")
                }
            }
        }
    }
}

fun gitShortSha(): String {
    return try {
        val process = ProcessBuilder("git", "rev-parse", "--short=7", "HEAD")
            .directory(rootDir)
            .redirectErrorStream(true)
            .start()
        val output = process.inputStream.bufferedReader().readText().trim()
        if (process.waitFor() == 0 && output.isNotEmpty()) output else "local"
    } catch (_: Exception) {
        "local"
    }
}
