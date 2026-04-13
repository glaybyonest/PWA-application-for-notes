# PWA Notes

Учебный проект по дисциплине «Фронтенд и бэкенд разработка». Приложение реализовано как PWA на Go и закрывает требования практик 13, 14, 15, 16 и 17.

## Что это за проект
Это приложение заметок и задач с такими возможностями:
- заметки хранятся в `localStorage`;
- приложение работает как PWA;
- есть Service Worker и офлайн-режим;
- есть installable manifest и иконки;
- backend написан на Go;
- realtime реализован через native WebSocket;
- push-уведомления реализованы через Web Push + VAPID;
- есть серверные напоминания и действие `Отложить на 5 минут`.

## Архитектура
- Backend: Go (`net/http`, `gorilla/websocket`, `webpush-go`)
- Frontend: vanilla HTML/CSS/JS
- Статика отдается тем же Go-сервером
- Заметки хранятся на клиенте в `localStorage`
- Push subscriptions и активные reminder-таймеры хранятся в памяти сервера

## Что выполнено по практикам
### Практика 13
Реализован базовый клиентский функционал заметок:
- форма создания обычной заметки;
- список заметок;
- сохранение и загрузка заметок из `localStorage`;
- защита от пустых заметок;
- мягкая нормализация старого формата заметок.

### Практика 14
Реализована PWA-основа и офлайн-поведение:
- App Shell;
- динамическая загрузка основного контента из `/content/home.html`;
- Service Worker;
- кэширование shell и контента;
- корректная работа приложения офлайн.

### Практика 15
Реализована installable PWA-конфигурация:
- `manifest.json`;
- набор иконок;
- запуск по HTTPS;
- корректная работа в secure context;
- возможность установки приложения как PWA.

### Практика 16
Реализован backend на Go и realtime:
- backend не использует Node.js и JavaScript;
- один Go-сервер отдает фронтенд и API;
- WebSocket endpoint `/ws`;
- клиент отправляет `newTask`;
- сервер рассылает `taskAdded`;
- в других вкладках показываются realtime-toast уведомления;
- push-подписка и отписка работают через сервер Go.

### Практика 17
Добавлена детализация push и напоминания:
- отдельная форма заметки с напоминанием;
- поле `datetime-local`;
- структура заметки с `id`, `text`, `reminder`;
- серверное планирование напоминаний на Go;
- push payload с `title`, `body`, `reminderId`;
- кнопка `Отложить на 5 минут`;
- endpoint для snooze и перепланирование reminder на 5 минут вперед.

## Структура проекта
```text
/
  cmd/server/main.go
  internal/config
  internal/httpserver
  internal/push
  internal/realtime
  internal/reminders
  web/
    index.html
    styles.css
    app.js
    sw.js
    manifest.json
    content/
      home.html
    icons/
  cert/
    .gitkeep
  go.mod
  README.md
```


## Требования
- Go 1.22+
- Chromium-based браузер для полной проверки PWA и push
- локальные сертификаты для `https://localhost:3000`
- `mkcert`

## Установка mkcert
После установки `mkcert` выполните:

```bash
mkcert -install
```

Windows:
```powershell
winget install --id FiloSottile.mkcert --source winget --accept-source-agreements --accept-package-agreements
```

macOS:
```bash
brew install mkcert nss
```

Linux:
Установите `mkcert` и `libnss3-tools` удобным для вашего дистрибутива способом.

## Генерация сертификатов
```bash
mkcert -key-file cert/localhost-key.pem -cert-file cert/localhost.pem localhost 127.0.0.1 ::1
```

Файлы должны лежать здесь:
- `cert/localhost.pem`
- `cert/localhost-key.pem`

## Запуск
Из корня проекта:

### Windows PowerShell
```powershell
$env:GOCACHE = (Join-Path (Get-Location) ".gocache")
go test ./...
go run ./cmd/server
```

### Linux / macOS
```bash
GOCACHE="$(pwd)/.gocache" go test ./...
go run ./cmd/server
```

Открывать:
```text
https://localhost:3000
```

### HTTP fallback
Если сертификатов пока нет:

```bash
go run ./cmd/server -allow-http
```

Открывать:
```text
http://localhost:3000
```

Для полной проверки PWA и push нужен именно HTTPS.

## Переменные окружения
- `APP_ADDR`
- `APP_WEB_DIR`
- `APP_CERT_FILE`
- `APP_KEY_FILE`
- `APP_ALLOW_HTTP`
- `APP_VAPID_SUBJECT`
- `APP_VAPID_PUBLIC_KEY_PATH`
- `APP_VAPID_PRIVATE_KEY_PATH`

По умолчанию VAPID-ключи сохраняются в:
- `cert/vapid_public.key`
- `cert/vapid_private.key`

## API
### HTTP
- `GET /api/config`
- `POST /api/push/subscribe`
- `POST /api/push/unsubscribe`
- `POST /api/reminders`
- `POST /api/reminders/snooze`
- `GET /ws`

### WebSocket
Клиент отправляет:
```json
{ "type": "newTask", "payload": { "id": "...", "text": "...", "datetime": "...", "createdAt": "...", "originClientId": "..." } }
```

Сервер рассылает:
```json
{ "type": "taskAdded", "payload": { "id": "...", "text": "...", "datetime": "...", "createdAt": "...", "originClientId": "..." } }
```

## Что где хранится
- Заметки: `localStorage`
- Push subscriptions: память Go-сервера
- Reminder-таймеры: память Go-сервера
- VAPID-ключи: `cert/`

Важно: после перезапуска сервера подписки и активные таймеры в памяти очищаются. Это допустимое ограничение учебной реализации, и оно честно сохранено в проекте.

## Как проверить
### 1. Обычная заметка
1. Откройте `https://localhost:3000`
2. Введите текст в первой форме
3. Нажмите `Добавить заметку`
4. Убедитесь, что заметка появилась в списке

### 2. Заметка с reminder
1. Заполните форму `Заметка с напоминанием`
2. Укажите время на 2-3 минуты вперед
3. Нажмите `Добавить с напоминанием`
4. Проверьте `localStorage`: у заметки должны быть `id`, `text`, `reminder`

### 3. WebSocket realtime
1. Откройте приложение в двух вкладках
2. Добавьте обычную заметку в первой вкладке
3. Во второй вкладке должен появиться toast
4. Индикатор сверху должен показывать активное соединение

### 4. Push-уведомления
1. Нажмите `Включить уведомления`
2. Разрешите уведомления в браузере
3. Добавьте обычную заметку
4. Убедитесь, что пришел системный push

### 5. Reminder и snooze
1. Создайте заметку с напоминанием
2. Дождитесь push
3. В уведомлении нажмите `Отложить на 5 минут`
4. Через 5 минут push должен прийти повторно

### 6. Offline
1. Загрузите приложение онлайн
2. Убедитесь, что Service Worker зарегистрирован
3. Отключите сеть
4. Перезагрузите страницу
5. Приложение должно открыться из кэша

## Полезные замечания
- Backend полностью на Go
- клиентские пути относительные
- WebSocket продолжает работать независимо от push-подписки
- push и snooze завязаны на Service Worker и поддержку браузером Push API
