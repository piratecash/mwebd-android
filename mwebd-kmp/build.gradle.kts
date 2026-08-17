import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    kotlin("multiplatform")
    id("com.android.library")
    `maven-publish`
}

group = "com.github.piratecash.mwebd-android"

kotlin {
    jvmToolchain(21)

    androidTarget {
        publishLibraryVariants("release")
        compilerOptions.jvmTarget.set(JvmTarget.JVM_17)
    }

    jvm("desktop") {
        compilerOptions.jvmTarget.set(JvmTarget.JVM_21)
    }

    sourceSets {
        getByName("commonMain").dependencies {
        }
        getByName("commonTest").dependencies {
            implementation(kotlin("test"))
        }
        getByName("androidMain").dependencies {
            implementation(project(":native-android"))
        }
        getByName("androidUnitTest").dependencies {
            implementation(kotlin("test"))
            implementation("junit:junit:4.13.2")
        }
        getByName("desktopMain").dependencies {
            implementation(project(":transport-jvm"))
        }
        getByName("desktopTest").dependencies {
            implementation(kotlin("test"))
            implementation("junit:junit:4.13.2")
        }
    }
}

android {
    namespace = "com.piratecash.mwebd"
    compileSdk = 35
    defaultConfig {
        minSdk = 24
    }
}

publishing {
    publications.withType<MavenPublication>().configureEach {
        pom {
            name.set("mwebd-kmp")
            description.set("Kotlin Multiplatform MWEB daemon client for Android and Desktop JVM")
            url.set("https://github.com/piratecash/mwebd-android")
            licenses {
                license {
                    name.set("MIT License")
                    url.set("https://opensource.org/licenses/MIT")
                }
            }
        }
    }
}
