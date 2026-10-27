-keep class mr.robot.agent.scrcpy.Server { public static void main(java.lang.String[]); }
-keep class mr.robot.agent.scrcpy.CleanUp { public static void main(java.lang.String[]); }
-keep class mr.robot.agent.shell.DisplayPower { *; }
-keep class android.view.IDisplayWindowListener** { *; }
-keep class android.content.IOnPrimaryClipChangedListener** { *; }
-keep class android.content.IContentProvider** { *; }
-dontwarn android.**

-dontobfuscate
-keep class mr.robot.agent.scrcpy.** { *; }
