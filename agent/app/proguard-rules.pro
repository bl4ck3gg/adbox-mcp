# Keep our entry point and every tool class. R8's whole-program analysis cannot
# see through string-driven dispatch like McpCore's tools/call.
-keep class bl4ck3gg.adbox.agent.** { *; }

# The VPN client is reached through the manifest (activity, service) and through
# app_process by class name, not by a static reference R8 can follow.
-keep class com.genymobile.gnirehtet.** { *; }

# Keep the reflection shims that talk to the Android framework.
-keep class mirror.** { *; }
-keep class proxy.** { *; }
-keep class uiautomator.** { *; }

# Framework classes are provided by the platform, never bundled.
-dontwarn android.**
-dontwarn androidx.**
