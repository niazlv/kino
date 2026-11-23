# R8 в релизе: только вырезание неиспользуемого кода — без переименования
# и без оптимизаций. Исходники открыты, прятать нечего, а трассы падений
# с телефона должны читаться как есть, без mapping.txt.
-dontobfuscate
# Оптимизации склеивают лямбды и классы и вместо имени файла пишут
# «r8-map-id-…»: без mapping трасса показывает чужие классы и неверные
# строки. Цена отказа — ~1,9 МБ (APK 18,6 МБ против 16,7).
-dontoptimize
# Номера строк в трассах (SourceFile/LineNumberTable R8 иначе выкидывает).
-keepattributes SourceFile,LineNumberTable

# kotlinx.serialization приносит свои правила; эти — страховка для наших
# классов (сериализаторы ищутся через Companion).
-keepattributes *Annotation*
-keepclassmembers class kotlinx.serialization.json.** { *** Companion; }
-keep,includedescriptorclasses class ru.sorewa.kino.**$$serializer { *; }
-keepclassmembers class ru.sorewa.kino.** { *** Companion; }
-keepclasseswithmembers class ru.sorewa.kino.** { kotlinx.serialization.KSerializer serializer(...); }
