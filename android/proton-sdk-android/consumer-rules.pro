# gomobile's runtime and the bound Go package are called from native code by
# name (JNI), so R8 must neither rename nor strip them.
-keep class go.** { *; }
-keep class org.tunaos.proton.gomobile.** { *; }
