import com.google.protobuf.gradle.id
import org.gradle.internal.os.OperatingSystem
import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    kotlin("jvm")
    id("com.google.protobuf")
    `java-library`
    `maven-publish`
}

group = "com.github.piratecash.mwebd-android"

java {
    toolchain.languageVersion.set(JavaLanguageVersion.of(21))
}

kotlin {
    compilerOptions.jvmTarget.set(JvmTarget.JVM_21)
}

val grpcVersion = "1.80.0"
val protobufVersion = "4.35.1"
val macosDeploymentTarget = "13.0"
val sidecarName = when {
    OperatingSystem.current().isWindows -> "mwebd-sidecar-windows-x64.exe"
    OperatingSystem.current().isMacOsX -> "mwebd-sidecar-macos-arm64"
    else -> "mwebd-sidecar-linux-x64"
}
val sidecarPlatform = when {
    OperatingSystem.current().isWindows -> "windows-x64"
    OperatingSystem.current().isMacOsX -> "macos-arm64"
    else -> "linux-x64"
}
val sidecarOutput = layout.buildDirectory.file("generated/sidecarResources/mwebd/$sidecarPlatform/$sidecarName")
val goPackageDir = rootProject.layout.projectDirectory.dir("go/mwebdandroid")
val commitSha = providers.environmentVariable("GITHUB_SHA").orElse(provider { gitCommitSha() })
val packagedSidecarsDirectory = providers.gradleProperty("sidecarAssetsDir")
    .orNull
    ?.let(rootProject::file)
val sidecarResourcesDirectory = packagedSidecarsDirectory
    ?: layout.buildDirectory.dir("generated/sidecarResources").get().asFile

dependencies {
    api("io.grpc:grpc-api:$grpcVersion")
    api("io.grpc:grpc-protobuf:$grpcVersion")
    api("io.grpc:grpc-stub:$grpcVersion")
    implementation("io.grpc:grpc-netty-shaded:$grpcVersion")
    implementation("com.google.protobuf:protobuf-java:$protobufVersion")
    compileOnly("javax.annotation:javax.annotation-api:1.3.2")
    testImplementation(kotlin("test-junit"))
    testImplementation("junit:junit:4.13.2")
}

sourceSets {
    main {
        proto.srcDir(rootProject.file("go/mwebdandroid/third_party/mwebd/proto"))
        resources.srcDir(sidecarResourcesDirectory)
    }
}

protobuf {
    protoc {
        artifact = "com.google.protobuf:protoc:$protobufVersion"
    }
    plugins {
        id("grpc") {
            artifact = "io.grpc:protoc-gen-grpc-java:$grpcVersion"
        }
    }
    generateProtoTasks {
        all().configureEach {
            plugins {
                id("grpc")
            }
        }
    }
}

val buildHostSidecar by tasks.registering(Exec::class) {
    workingDir = goPackageDir.asFile
    inputs.files(rootProject.fileTree(goPackageDir))
    inputs.property("artifactVersion", project.version)
    inputs.property("commitSha", commitSha)
    inputs.property("macosDeploymentTarget", macosDeploymentTarget)
    outputs.file(sidecarOutput)

    doFirst {
        sidecarOutput.get().asFile.parentFile.mkdirs()
    }

    environment("CGO_ENABLED", "1")
    if (OperatingSystem.current().isMacOsX) {
        environment("MACOSX_DEPLOYMENT_TARGET", macosDeploymentTarget)
        environment("CGO_CFLAGS", "-mmacosx-version-min=$macosDeploymentTarget")
        environment("CGO_LDFLAGS", "-mmacosx-version-min=$macosDeploymentTarget")
    }
    commandLine(
        "go",
        "build",
        "-trimpath",
        "-ldflags",
        "-s -w " +
            "-X github.com/piratecash/mwebd-android/go/mwebdandroid.artifactVersion=${project.version} " +
            "-X github.com/piratecash/mwebd-android/go/mwebdandroid.commitSHA=${commitSha.get()}",
        "-o",
        sidecarOutput.get().asFile.absolutePath,
        "./cmd/mwebdsidecar",
    )
}

tasks.processResources {
    if (packagedSidecarsDirectory == null) {
        dependsOn(buildHostSidecar)
    } else {
        inputs.dir(packagedSidecarsDirectory)
        doFirst {
            check(packagedSidecarsDirectory.resolve("mwebd").isDirectory) {
                "sidecarAssetsDir must contain the verified mwebd resource directory"
            }
        }
    }
}

tasks.test {
    useJUnit()
}

tasks.jar {
    manifest.attributes["Implementation-Version"] = project.version.toString()
}

publishing {
    publications {
        create<MavenPublication>("mavenJava") {
            from(components["java"])
            artifactId = "transport-jvm"
            pom {
                name.set("mwebd desktop transport")
                description.set("Authenticated desktop sidecar transport for mwebd-kmp")
            }
        }
    }
}

fun gitCommitSha(): String {
    return try {
        val process = ProcessBuilder("git", "rev-parse", "HEAD")
            .directory(rootProject.projectDir)
            .redirectErrorStream(true)
            .start()
        val output = process.inputStream.bufferedReader().readText().trim()
        if (process.waitFor() == 0 && output.isNotEmpty()) output else "unknown"
    } catch (_: Exception) {
        "unknown"
    }
}
