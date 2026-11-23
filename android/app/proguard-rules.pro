# R8 в релизе: только сжатие и оптимизация, без переименования.
# Исходники открыты, прятать нечего, а с исходными именами классов
# трассы падений с телефона читаются как есть и ничто, что ищет класс
# по имени, не ломается. Выигрыш в размере почти весь от вырезания
# неиспользуемого кода, переименование дало бы немного сверху.
-dontobfuscate
# Номера строк в трассах (SourceFile/LineNumberTable R8 иначе выкидывает).
-keepattributes SourceFile,LineNumberTable

# kotlinx.serialization приносит свои правила; эти — страховка для наших
# классов (сериализаторы ищутся через Companion).
-keepattributes *Annotation*
-keepclassmembers class kotlinx.serialization.json.** { *** Companion; }
-keep,includedescriptorclasses class ru.sorewa.kino.**$$serializer { *; }
-keepclassmembers class ru.sorewa.kino.** { *** Companion; }
-keepclasseswithmembers class ru.sorewa.kino.** { kotlinx.serialization.KSerializer serializer(...); }
