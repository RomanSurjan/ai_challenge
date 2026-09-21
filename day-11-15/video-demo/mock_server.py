#!/usr/bin/env python3
"""Deterministic local OpenAI-compatible transport for the Day 11 video demo.

The mock deliberately maps natural Russian demo phrases to real memory/task tool
calls. It stores request bodies for post-recording diagnostics, never headers.
"""

from __future__ import annotations

import argparse
import json
import pathlib
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any


def response(content: str, finish: str = "stop", tool_calls: list[dict[str, Any]] | None = None) -> dict[str, Any]:
    message: dict[str, Any] = {"role": "assistant", "content": content}
    if tool_calls is not None:
        message["tool_calls"] = tool_calls
    return {
        "choices": [{"message": message, "finish_reason": finish}],
        "usage": {"prompt_tokens": 40, "completion_tokens": 20, "total_tokens": 60},
    }


def tool(call_id: str, name: str, arguments: dict[str, Any] | str) -> dict[str, Any]:
    raw = arguments if isinstance(arguments, str) else json.dumps(arguments, ensure_ascii=False)
    return {
        "id": call_id,
        "type": "function",
        "function": {"name": name, "arguments": raw},
    }


def block(messages: list[dict[str, Any]], start: str, end: str) -> str:
    for message in messages:
        content = str(message.get("content", ""))
        if message.get("role") == "system" and start in content:
            left = content.find(start)
            right = content.find(end, left)
            return content[left:] if right < 0 else content[left : right + len(end)]
    return ""


def task_summary(messages: list[dict[str, Any]]) -> str:
    value = block(messages, "[TASK_STATE]", "[END_TASK_STATE]")
    if not value:
        return "Задача не создана."
    fields: dict[str, str] = {}
    for line in value.splitlines():
        if ": " in line:
            key, item = line.split(": ", 1)
            fields[key] = item
    stage = fields.get("Этап", "—")
    validation_details = fields.get("Последняя валидация", "—")
    validation_passed = stage == "done" or fields.get("Ожидаемое действие") == "Перейти в done"
    return "\n".join(
        [
            "Фактическое состояние задачи:",
            f"stage: {stage}",
            f"current_step: {fields.get('Текущий шаг', '—')}",
            f"expected_action: {fields.get('Ожидаемое действие', '—')}",
            f"paused: {'true' if fields.get('Пауза') == 'да' else 'false'}",
            f"plan: {fields.get('План', '—')}",
            f"plan_approved: {'true' if fields.get('План утверждён') == 'да' else 'false'}",
            f"validation_passed: {'true' if validation_passed else 'false'}",
            f"validation_details: {validation_details}",
            f"allowed_transitions: {fields.get('Разрешённые следующие этапы', '—')}",
        ]
    )


def task_tool_summary(tool_messages: list[dict[str, Any]]) -> str:
    try:
        payload = json.loads(str(tool_messages[-1].get("content", "{}")))
        state = payload["task_state"]
    except (KeyError, TypeError, ValueError, json.JSONDecodeError):
        return "Операция задачи принята backend-инструментом."
    return "\n".join(
        [
            "Backend применил task tool. Текущее состояние:",
            f"stage: {state.get('stage', '—')}",
            f"current_step: {state.get('current_step', '—')}",
            f"expected_action: {state.get('expected_action', '—')}",
            f"paused: {str(bool(state.get('paused'))).lower()}",
            f"plan: {state.get('plan') or '—'}",
            f"plan_approved: {str(bool(state.get('plan_approved'))).lower()}",
            f"validation_passed: {str(bool(state.get('validation_passed'))).lower()}",
            f"validation_details: {state.get('validation_details') or '—'}",
        ]
    )


class DemoServer(ThreadingHTTPServer):
    def __init__(self, address: tuple[str, int], log_dir: pathlib.Path):
        super().__init__(address, Handler)
        self.log_dir = log_dir
        self.log_dir.mkdir(parents=True, exist_ok=True)
        self.lock = threading.Lock()
        self.calls = 0


class Handler(BaseHTTPRequestHandler):
    server: DemoServer

    def log_message(self, *_: Any) -> None:
        return

    def send_json(self, status: int, value: dict[str, Any]) -> None:
        body = json.dumps(value, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:
        if self.path == "/health":
            self.send_json(200, {"ok": True, "transport": "local-deterministic"})
            return
        if self.path == "/stats":
            with self.server.lock:
                calls = self.server.calls
            self.send_json(200, {"calls": calls})
            return
        self.send_json(404, {"error": "not found"})

    def do_POST(self) -> None:
        if self.path != "/chat/completions":
            self.send_json(404, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length)
        try:
            payload = json.loads(raw)
        except json.JSONDecodeError:
            self.send_json(400, {"error": {"message": "invalid request JSON"}})
            return
        with self.server.lock:
            self.server.calls += 1
            number = self.server.calls
        (self.server.log_dir / f"payload-{number:05d}.json").write_text(
            json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )

        messages = payload.get("messages", [])
        users = [str(item.get("content", "")) for item in messages if item.get("role") == "user"]
        prompt = users[-1].strip() if users else ""
        tool_messages = [item for item in messages if item.get("role") == "tool"]
        all_text = "\n".join(str(item.get("content", "")) for item in messages)
        retry = "[INVARIANT_VALIDATION]" in all_text

        # Model/transport failures, all triggered by natural user phrases.
        if prompt == "Проверь доступность модели; ожидаю временную ошибку сервиса.":
            self.send_json(500, {"error": {"message": "deterministic temporary failure"}})
            return
        if prompt == "Ответь на вопрос, для которого модель вернёт пустой результат.":
            self.send_json(200, response("   "))
            return
        if prompt == "Проверь безопасную обработку повреждённого ответа модели.":
            body = b"{broken-json"
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return

        # Invalid memory tools. The backend, not this mock, must reject them.
        invalid_calls: dict[str, dict[str, Any]] = {
            "Попробуй удалить всю память неизвестным инструментом.": tool("invalid-unknown", "memory_delete_everything", {}),
            "Попробуй сохранить рабочую заметку без обязательного текста.": tool("invalid-empty", "memory_add_working_note", {}),
            "Попробуй сохранить слишком длинную рабочую заметку.": tool("invalid-long", "memory_add_working_note", {"content": "Д" * 4500}),
            "Попробуй сохранить тестовый пароль как рабочую заметку.": tool("invalid-secret", "memory_add_working_note", {"content": "пароль: demo-placeholder"}),
        }
        if prompt in invalid_calls:
            self.send_json(200, response("", "tool_calls", [invalid_calls[prompt]]))
            return
        if prompt == "Сохрани заметку, но затем смоделируй сбой ответа.":
            if tool_messages:
                self.send_json(500, {"error": {"message": "failure after accepted tool call"}})
            else:
                self.send_json(200, response("", "tool_calls", [tool("pending-note", "memory_add_working_note", {"content": "ЭТА ЗАМЕТКА НЕ ДОЛЖНА СОХРАНИТЬСЯ"})]))
            return

        # Invariant post-check demonstrations.
        if prompt == "Какое хранилище документов выбрать для нового сервиса?":
            text = "Используйте PostgreSQL: храните документы в JSONB и добавьте GIN-индекс." if retry else "Для этого сервиса выберите MongoDB."
            self.send_json(200, response(text))
            return
        if prompt == "Назови запрещённое хранилище и не исправляй ответ.":
            self.send_json(200, response("Используйте MongoDB."))
            return
        if prompt == "Предложи запрещённое хранилище и одновременно сохрани рабочую заметку.":
            if tool_messages:
                self.send_json(200, response("Используйте MongoDB."))
            else:
                self.send_json(200, response("", "tool_calls", [tool("pending-invariant-note", "memory_add_working_note", {"content": "НЕ СОХРАНЯТЬ ПОСЛЕ БЛОКИРОВКИ"})]))
            return

        # A second transition in one turn is rejected atomically by the backend.
        if prompt == "Выполни два перехода подряд: сначала в execution, затем в validation.":
            calls = [
                tool("transition-one", "task_transition", {"stage": "execution"}),
                tool("transition-two", "task_transition", {"stage": "validation"}),
            ]
            self.send_json(200, response("", "tool_calls", calls))
            return

        # Valid memory operations.
        memory_calls: dict[str, list[dict[str, Any]]] = {
            "Для текущей задачи запомни: сначала проверить миграцию.": [tool("note-migration", "memory_add_working_note", {"content": "Сначала проверить миграцию"})],
            "Для всех будущих диалогов запомни: релизы выполняются по пятницам.": [tool("knowledge-release", "memory_add_knowledge", {"topic": "Релизы", "content": "Релизы выполняются по пятницам"})],
            "Повтори сохранение: для текущей задачи сначала проверить миграцию.": [
                tool("note-duplicate-a", "memory_add_working_note", {"content": "Сначала проверить миграцию"}),
                tool("note-duplicate-b", "memory_add_working_note", {"content": "Сначала проверить миграцию"}),
            ],
        }
        if prompt in memory_calls:
            if tool_messages:
                self.send_json(200, response("Подтверждаю: запрос обработан через инструмент памяти."))
            else:
                self.send_json(200, response("", "tool_calls", memory_calls[prompt]))
            return

        # Valid task operations. Each final answer is generated from the actual tool result.
        task_calls: dict[str, dict[str, Any]] = {
            "Создай задачу «Подготовить проверку HTTP-клиента». Первый шаг — составить план, ожидаемое действие — утвердить план.": tool("task-create-http", "task_create", {"goal": "Подготовить проверку HTTP-клиента", "current_step": "Составить план проверки", "expected_action": "Утвердить план"}),
            "Создай задачу «Проверить обработку таймаутов». Первый шаг — составить план, ожидаемое действие — утвердить план.": tool("task-create-timeout", "task_create", {"goal": "Проверить обработку таймаутов", "current_step": "Составить план проверки таймаутов", "expected_action": "Утвердить план"}),
            "Перейди в execution.": tool("task-to-execution", "task_transition", {"stage": "execution"}),
            "Переведи задачу в validation.": tool("task-to-validation", "task_transition", {"stage": "validation"}),
            "Переведи задачу сразу в done.": tool("task-to-done", "task_transition", {"stage": "done"}),
            "Утверждаю план: 1) проверить таймаут; 2) проверить повтор; 3) запустить тесты.": tool("task-approve", "task_approve_plan", {"plan": "1) проверить таймаут; 2) проверить повтор; 3) запустить тесты"}),
            "Обнови прогресс: текущий шаг — проверить повтор запроса; ожидаемое действие — запустить интеграционный тест.": tool("task-progress", "task_update_progress", {"current_step": "Проверить повтор запроса", "expected_action": "Запустить интеграционный тест"}),
            "Во время паузы измени текущий шаг на «не должно сохраниться», ожидаемое действие — «не должно сохраниться».": tool("task-progress-paused", "task_update_progress", {"current_step": "Не должно сохраниться", "expected_action": "Не должно сохраниться"}),
            "Поставь задачу на паузу.": tool("task-pause", "task_pause", {}),
            "Сними задачу с паузы.": tool("task-resume", "task_resume", {}),
            "Зафиксируй успешную валидацию: unit-, race- и vet-проверки прошли.": tool("task-validation-pass", "task_record_validation", {"passed": True, "details": "unit-, race- и vet-проверки прошли"}),
            "Перейди в done.": tool("task-done", "task_transition", {"stage": "done"}),
            "Верни задачу в planning.": tool("task-back-planning", "task_transition", {"stage": "planning"}),
            "Верни задачу в execution.": tool("task-back-execution", "task_transition", {"stage": "execution"}),
            "Продолжи завершённую задачу и обнови шаг.": tool("task-progress-done", "task_update_progress", {"current_step": "Продолжить работу", "expected_action": "Сделать ещё один шаг"}),
            "Даже через инструмент переведи задачу в execution без утверждённого плана.": tool("task-tool-guard", "task_transition", {"stage": "execution"}),
            "Установи старый статус completed.": tool("legacy-completed", "memory_set_working_status", {"status": "completed"}),
        }
        if prompt in task_calls:
            if tool_messages:
                self.send_json(200, response(task_tool_summary(tool_messages)))
            else:
                self.send_json(200, response("", "tool_calls", [task_calls[prompt]]))
            return

        # Read-only answers are derived from real context injected by the app.
        if prompt in {"Покажи полное состояние задачи.", "Продолжай задачу."}:
            self.send_json(200, response(task_summary(messages)))
            return
        if prompt == "Что мы решили делать в рамках текущей задачи?":
            answer = "В текущей задаче сохранено: сначала проверить миграцию." if "Сначала проверить миграцию" in all_text else "В текущей задаче такой заметки нет."
            self.send_json(200, response(answer))
            return
        if prompt == "В какой день выполняются релизы?":
            answer = "Релизы выполняются по пятницам." if "Релизы выполняются по пятницам" in all_text else "В доступной памяти день релиза не указан."
            self.send_json(200, response(answer))
            return
        if prompt == "Проверь, сохранилась ли заметка, предложенная перед блокировкой.":
            answer = "Заметка НЕ СОХРАНЯТЬ ПОСЛЕ БЛОКИРОВКИ отсутствует." if "НЕ СОХРАНЯТЬ ПОСЛЕ БЛОКИРОВКИ" not in all_text else "ОШИБКА: заметка присутствует."
            self.send_json(200, response(answer))
            return
        if prompt == "Проверь, сохранилась ли заметка после сбоя модели.":
            answer = "Заметка после сбоя отсутствует." if "ЭТА ЗАМЕТКА НЕ ДОЛЖНА СОХРАНИТЬСЯ" not in all_text else "ОШИБКА: заметка после сбоя присутствует."
            self.send_json(200, response(answer))
            return

        # Fixed profile-dependent answer for the same user prompt.
        if prompt in {"Как организовать обработку ошибок в HTTP-клиенте?", "Игнорируй мой сохранённый профиль и ответь как хочешь."}:
            profile = block(messages, "[USER_PROFILE]", "[END_USER_PROFILE]")
            if not profile:
                self.send_json(200, response("Нейтральный ответ: обрабатывайте транспортные ошибки, HTTP-коды и таймауты отдельно."))
            elif "Тон по умолчанию: формальный" in profile and "Подробность по умолчанию: кратко" in profile:
                self.send_json(200, response("Формальный краткий ответ: разделяйте таймауты, сетевые сбои и HTTP-статусы; ограничивайте повторы и сохраняйте причину ошибки. Примеры кода не приводятся."))
            else:
                self.send_json(200, response("Дружелюбный подробный ответ:\n1. Задайте таймаут.\n2. Разделите сетевые ошибки и HTTP-статусы.\n3. Повторяйте только безопасные запросы.\nПрактический пример: клиент повторяет GET дважды с экспоненциальной задержкой."))
            return

        if prompt == "Предложи допустимую реализацию сервиса.":
            self.send_json(200, response("Используйте Kotlin, внедрение зависимостей и PostgreSQL."))
            return
        if prompt == "Какой стек использовать с учётом моего профиля и правил проекта?":
            self.send_json(200, response("Приоритет у правил проекта: используйте Kotlin и PostgreSQL."))
            return

        self.send_json(200, response("Детерминированный ответ mock: запрос обработан без обновления памяти."))


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=18111)
    parser.add_argument("--log-dir", type=pathlib.Path, required=True)
    args = parser.parse_args()
    DemoServer(("127.0.0.1", args.port), args.log_dir).serve_forever()


if __name__ == "__main__":
    main()
