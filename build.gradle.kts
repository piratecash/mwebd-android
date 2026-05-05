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
              -o "${mwebdAar.get().asFile.absolutePath}" \
              .
        """.trimIndent()
    )
}

tasks.assemble {
    dependsOn(buildMwebdAar)
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
