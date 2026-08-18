plugins {
    base
    `maven-publish`
    kotlin("multiplatform") version "2.3.20" apply false
    kotlin("jvm") version "2.3.20" apply false
    id("com.android.library") version "8.8.2" apply false
    id("com.google.protobuf") version "0.10.0" apply false
}

val semver = Regex("(?:0|[1-9][0-9]*)\\.(?:0|[1-9][0-9]*)\\.(?:0|[1-9][0-9]*)")

group = "com.github.piratecash"
version = providers.environmentVariable("JITPACK_VERSION")
    .orElse(providers.environmentVariable("VERSION"))
    .orElse(providers.environmentVariable("VERSION_NAME"))
    .orElse(providers.gradleProperty("VERSION_NAME"))
    .orElse("0.0.0-SNAPSHOT")
    .get()

require(version.toString() == "0.0.0-SNAPSHOT" || semver.matches(version.toString())) {
    "VERSION_NAME must be exact numeric SemVer: MAJOR.MINOR.PATCH"
}

allprojects {
    version = rootProject.version
}

val nativeAndroid = project(":native-android")
val mwebdAar = nativeAndroid.layout.buildDirectory.file("outputs/aar/mwebd-android.aar")
val buildMwebdAar by tasks.registering {
    dependsOn(":native-android:buildMwebdAar")
}
val verifyElfAlignment by tasks.registering {
    dependsOn(":native-android:verifyElfAlignment")
}

tasks.assemble {
    dependsOn(":mwebd-kmp:assemble", verifyElfAlignment, ":transport-jvm:assemble")
}

tasks.check {
    dependsOn(":mwebd-kmp:allTests", ":native-android:check", ":transport-jvm:check")
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

tasks.named("publishToMavenLocal") {
    dependsOn(
        ":mwebd-kmp:publishToMavenLocal",
        ":native-android:publishToMavenLocal",
        ":transport-jvm:publishToMavenLocal",
    )
}
