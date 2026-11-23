# Правила для будущей минификации релиза. Пока minifyEnabled false —
# но правила описываем заранее, чтобы включение не сломало сериализацию.
-keepattributes *Annotation*
-keepclassmembers class kotlinx.serialization.json.** { *** Companion; }
-keep,includedescriptorclasses class ru.sorewa.kino.**$$serializer { *; }
-keepclassmembers class ru.sorewa.kino.** { *** Companion; }
-keepclasseswithmembers class ru.sorewa.kino.** { kotlinx.serialization.KSerializer serializer(...); }
