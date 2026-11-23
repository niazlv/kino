// Дефолтный интерфейс плеера — опциональный и tree-shakeable.
// Приложение вправе не импортировать ничего отсюда и нарисовать свой:
// ядро от интерфейса не зависит.
export {
  Controls, readSkipMode, SKIP_KEY, type ControlsProps, type NextItem, type ChoiceGroup, type SkipMode,
} from './Controls'
