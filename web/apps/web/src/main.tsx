import React from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import './styles.css'
import { installTV } from './tv'
import { installLibraryEvents, installPip } from './native'

// Стрелки, «назад» и раскладка телевизора — до первого кадра.
installTV()
// Форточка в приложении: оболочке нужно знать, идёт ли просмотр.
installPip()
// Оболочка сообщает, что в скачанном появилось новое (кадры, старые файлы).
installLibraryEvents()

createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
