#!/usr/bin/env python3
"""Local, dependency-free web chat proxy for Ollama.

The browser only talks to this server. The server validates the complete chat
history, prepends the configured system prompt, and streams normalized NDJSON
events from the local Ollama /api/chat endpoint.
"""

from __future__ import annotations

import argparse
import json
import os
import socket
import sys
import urllib.error
import urllib.request
from dataclasses import dataclass
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any, Iterator
from urllib.parse import urlparse


DEFAULT_HOST = "127.0.0.1"
DEFAULT_PORT = 8080
DEFAULT_OLLAMA_URL = "http://127.0.0.1:11434"
DEFAULT_MODEL = "qwen2.5:3b"
DEFAULT_SYSTEM_PROMPT = (
    "Ты полезный локальный ассистент. Отвечай точно, ясно и на языке пользователя."
)

MAX_REQUEST_BYTES = 256 * 1024
MAX_MESSAGE_CHARS = 12_000
MAX_SYSTEM_PROMPT_CHARS = 4_000
MAX_HISTORY_MESSAGES = 64
MAX_HISTORY_CHARS = 60_000
MAX_OLLAMA_RESPONSE_BYTES = 2 * 1024 * 1024
MAX_OLLAMA_LINE_BYTES = 256 * 1024
CONNECT_TIMEOUT_SECONDS = 5.0
READ_TIMEOUT_SECONDS = 180.0

ROOT_DIR = Path(__file__).resolve().parent
WEB_DIR = ROOT_DIR / "web"


class RequestValidationError(ValueError):
    """A safe validation error that can be returned to the browser."""

    def __init__(self, message: str, status: int = HTTPStatus.BAD_REQUEST):
        super().__init__(message)
        self.status = int(status)


class OllamaError(RuntimeError):
    """A safe, user-facing failure while talking to local Ollama."""


class ModelUnavailableError(OllamaError):
    """The configured model is absent from Ollama's local model list."""


@dataclass(frozen=True)
class ServerConfig:
    model: str = DEFAULT_MODEL
    system_prompt: str = DEFAULT_SYSTEM_PROMPT
    ollama_url: str = DEFAULT_OLLAMA_URL
    connect_timeout: float = CONNECT_TIMEOUT_SECONDS
    read_timeout: float = READ_TIMEOUT_SECONDS


def _json_bytes(value: Any) -> bytes:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def _safe_error_detail(raw: bytes) -> str:
    text = raw[:4096].decode("utf-8", errors="replace").strip()
    if not text:
        return "пустой ответ"
    try:
        value = json.loads(text)
    except json.JSONDecodeError:
        return text
    if isinstance(value, dict) and isinstance(value.get("error"), str):
        return value["error"]
    return text


def _validate_ollama_url(url: str) -> str:
    parsed = urlparse(url)
    if parsed.scheme != "http" or parsed.hostname != "127.0.0.1":
        raise ValueError("Ollama URL must use local http://127.0.0.1")
    if parsed.path not in ("", "/") or parsed.query or parsed.fragment:
        raise ValueError("Ollama URL must not contain a path, query, or fragment")
    try:
        _ = parsed.port
    except ValueError as exc:
        raise ValueError("Ollama URL contains an invalid port") from exc
    return url.rstrip("/")


class OllamaStream:
    """A closeable iterator over a single Ollama streaming response."""

    def __init__(self, response: Any):
        self._response = response
        self.finished = False

    def __iter__(self) -> Iterator[dict[str, Any]]:
        try:
            while True:
                raw = self._response.readline(MAX_OLLAMA_LINE_BYTES + 1)
                if not raw:
                    break
                if len(raw) > MAX_OLLAMA_LINE_BYTES:
                    raise OllamaError("Ollama вернул слишком длинное событие потока")
                if not raw.strip():
                    continue
                try:
                    event = json.loads(raw)
                except json.JSONDecodeError as exc:
                    raise OllamaError("Ollama вернул некорректный поток JSON") from exc
                if not isinstance(event, dict):
                    raise OllamaError("Ollama вернул неожиданное событие потока")
                if isinstance(event.get("error"), str):
                    raise OllamaError(f"Ошибка Ollama: {event['error']}")
                yield event
                if event.get("done") is True:
                    self.finished = True
                    return
            if not self.finished:
                raise OllamaError("Поток Ollama завершился без финального события")
        except (TimeoutError, socket.timeout) as exc:
            raise OllamaError("Ollama не ответил вовремя") from exc
        except OSError as exc:
            raise OllamaError(f"Соединение с Ollama прервано: {exc}") from exc
        finally:
            self.close()

    def close(self) -> None:
        response, self._response = self._response, None
        if response is not None:
            response.close()


class OllamaClient:
    def __init__(self, config: ServerConfig):
        self.config = config
        self.base_url = _validate_ollama_url(config.ollama_url)

    def _request_json(self, path: str) -> dict[str, Any]:
        request = urllib.request.Request(
            f"{self.base_url}{path}",
            headers={"Accept": "application/json"},
            method="GET",
        )
        try:
            with urllib.request.urlopen(
                request, timeout=self.config.connect_timeout
            ) as response:
                raw = response.read(MAX_OLLAMA_RESPONSE_BYTES + 1)
        except urllib.error.HTTPError as exc:
            try:
                detail = _safe_error_detail(exc.read(4096))
            finally:
                exc.close()
            raise OllamaError(f"Ollama вернул HTTP {exc.code}: {detail}") from exc
        except (urllib.error.URLError, TimeoutError, socket.timeout, OSError) as exc:
            raise OllamaError(
                f"Ollama недоступен по адресу {self.base_url}: {exc}"
            ) from exc
        if len(raw) > MAX_OLLAMA_RESPONSE_BYTES:
            raise OllamaError("Служебный ответ Ollama слишком велик")
        try:
            value = json.loads(raw)
        except json.JSONDecodeError as exc:
            raise OllamaError("Ollama вернул некорректный JSON") from exc
        if not isinstance(value, dict):
            raise OllamaError("Ollama вернул JSON неожиданного типа")
        return value

    def installed_models(self) -> list[str]:
        payload = self._request_json("/api/tags")
        items = payload.get("models")
        if not isinstance(items, list):
            raise OllamaError("В ответе Ollama отсутствует список моделей")
        names: list[str] = []
        for item in items:
            if not isinstance(item, dict):
                continue
            name = item.get("name") or item.get("model")
            if isinstance(name, str):
                names.append(name)
        return names

    def ensure_model_available(self) -> None:
        names = self.installed_models()
        if self.config.model not in names:
            raise ModelUnavailableError(
                f"Модель {self.config.model} не установлена. "
                f"Выполните: ollama pull {self.config.model}"
            )

    def status(self) -> dict[str, Any]:
        try:
            version = self._request_json("/api/version").get("version")
            names = self.installed_models()
        except OllamaError as exc:
            return {
                "connected": False,
                "model": self.config.model,
                "modelAvailable": False,
                "version": None,
                "error": str(exc),
            }
        available = self.config.model in names
        result: dict[str, Any] = {
            "connected": True,
            "model": self.config.model,
            "modelAvailable": available,
            "version": version if isinstance(version, str) else None,
        }
        if not available:
            result["error"] = (
                f"Модель {self.config.model} не установлена. "
                f"Выполните: ollama pull {self.config.model}"
            )
        return result

    def open_chat_stream(self, messages: list[dict[str, str]]) -> OllamaStream:
        payload = {
            "model": self.config.model,
            "messages": messages,
            "stream": True,
            "keep_alive": "10m",
        }
        request = urllib.request.Request(
            f"{self.base_url}/api/chat",
            data=_json_bytes(payload),
            headers={
                "Accept": "application/x-ndjson",
                "Content-Type": "application/json; charset=utf-8",
            },
            method="POST",
        )
        try:
            response = urllib.request.urlopen(
                request, timeout=self.config.read_timeout
            )
        except urllib.error.HTTPError as exc:
            try:
                detail = _safe_error_detail(exc.read(4096))
            finally:
                exc.close()
            if exc.code == HTTPStatus.NOT_FOUND and "model" in detail.lower():
                raise ModelUnavailableError(
                    f"Модель {self.config.model} недоступна: {detail}"
                ) from exc
            raise OllamaError(f"Ollama вернул HTTP {exc.code}: {detail}") from exc
        except (urllib.error.URLError, TimeoutError, socket.timeout, OSError) as exc:
            raise OllamaError(
                f"Не удалось начать генерацию через Ollama: {exc}"
            ) from exc
        return OllamaStream(response)


def validate_chat_payload(
    payload: Any, default_system_prompt: str
) -> tuple[list[dict[str, str]], str]:
    if not isinstance(payload, dict):
        raise RequestValidationError("Корневое значение JSON должно быть объектом")

    messages = payload.get("messages")
    if not isinstance(messages, list):
        raise RequestValidationError("Поле messages должно быть массивом")
    if not messages:
        raise RequestValidationError("История диалога пуста")
    if len(messages) > MAX_HISTORY_MESSAGES:
        raise RequestValidationError(
            f"В истории может быть не более {MAX_HISTORY_MESSAGES} сообщений",
            HTTPStatus.REQUEST_ENTITY_TOO_LARGE,
        )

    validated: list[dict[str, str]] = []
    total_chars = 0
    expected_role = "user"
    for index, item in enumerate(messages):
        if not isinstance(item, dict):
            raise RequestValidationError(
                f"Сообщение {index + 1} должно быть объектом"
            )
        role = item.get("role")
        content = item.get("content")
        if role not in ("user", "assistant"):
            raise RequestValidationError(
                f"Сообщение {index + 1}: допустимы роли user и assistant"
            )
        if role != expected_role:
            raise RequestValidationError(
                "Сообщения должны чередоваться, начиная с сообщения пользователя"
            )
        if not isinstance(content, str):
            raise RequestValidationError(
                f"Сообщение {index + 1}: content должен быть строкой"
            )
        if not content.strip():
            raise RequestValidationError(f"Сообщение {index + 1} пусто")
        if len(content) > MAX_MESSAGE_CHARS:
            raise RequestValidationError(
                f"Сообщение {index + 1} длиннее {MAX_MESSAGE_CHARS} символов",
                HTTPStatus.REQUEST_ENTITY_TOO_LARGE,
            )
        total_chars += len(content)
        if total_chars > MAX_HISTORY_CHARS:
            raise RequestValidationError(
                f"История длиннее {MAX_HISTORY_CHARS} символов",
                HTTPStatus.REQUEST_ENTITY_TOO_LARGE,
            )
        validated.append({"role": role, "content": content})
        expected_role = "assistant" if role == "user" else "user"

    if validated[-1]["role"] != "user":
        raise RequestValidationError(
            "Последним должно быть новое сообщение пользователя"
        )

    if "systemPrompt" in payload:
        system_prompt = payload["systemPrompt"]
    else:
        system_prompt = default_system_prompt
    if not isinstance(system_prompt, str):
        raise RequestValidationError("Поле systemPrompt должно быть строкой")
    if len(system_prompt) > MAX_SYSTEM_PROMPT_CHARS:
        raise RequestValidationError(
            f"Системный промт длиннее {MAX_SYSTEM_PROMPT_CHARS} символов",
            HTTPStatus.REQUEST_ENTITY_TOO_LARGE,
        )
    return validated, system_prompt


class ChatHTTPServer(ThreadingHTTPServer):
    allow_reuse_address = True
    daemon_threads = True

    def __init__(
        self,
        server_address: tuple[str, int],
        config: ServerConfig,
        web_dir: Path = WEB_DIR,
    ):
        self.config = config
        self.ollama = OllamaClient(config)
        self.web_dir = web_dir.resolve()
        super().__init__(server_address, ChatRequestHandler)


class ChatRequestHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "Day26-27LocalChat/1.0"

    @property
    def chat_server(self) -> ChatHTTPServer:
        return self.server  # type: ignore[return-value]

    def log_message(self, format_string: str, *args: Any) -> None:
        print(
            f"[{self.log_date_time_string()}] {self.client_address[0]} "
            + format_string % args,
            file=sys.stderr,
        )

    def _security_headers(self) -> None:
        self.send_header("X-Content-Type-Options", "nosniff")
        self.send_header("Referrer-Policy", "no-referrer")
        self.send_header("X-Frame-Options", "DENY")
        self.send_header(
            "Content-Security-Policy",
            "default-src 'self'; script-src 'self'; style-src 'self'; "
            "connect-src 'self'; img-src 'self' data:; base-uri 'none'; "
            "frame-ancestors 'none'; form-action 'self'",
        )

    def _send_json(
        self, status: int, value: dict[str, Any], *, close: bool = False
    ) -> None:
        body = _json_bytes(value)
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        if close:
            self.send_header("Connection", "close")
            self.close_connection = True
        self._security_headers()
        self.end_headers()
        if self.command != "HEAD":
            try:
                self.wfile.write(body)
            except (BrokenPipeError, ConnectionResetError):
                self.close_connection = True

    def _send_error_json(self, status: int, message: str) -> None:
        self._send_json(status, {"error": message}, close=True)

    def _serve_static(self, filename: str, content_type: str) -> None:
        path = (self.chat_server.web_dir / filename).resolve()
        if path.parent != self.chat_server.web_dir or not path.is_file():
            self._send_error_json(HTTPStatus.NOT_FOUND, "Файл не найден")
            return
        try:
            body = path.read_bytes()
        except OSError as exc:
            self.log_error("cannot read static file: %s", exc)
            self._send_error_json(
                HTTPStatus.INTERNAL_SERVER_ERROR, "Не удалось прочитать файл интерфейса"
            )
            return
        self.send_response(HTTPStatus.OK)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-cache")
        self._security_headers()
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def do_HEAD(self) -> None:
        self.do_GET()

    def do_GET(self) -> None:
        route = urlparse(self.path).path
        static_routes = {
            "/": ("index.html", "text/html; charset=utf-8"),
            "/index.html": ("index.html", "text/html; charset=utf-8"),
            "/styles.css": ("styles.css", "text/css; charset=utf-8"),
            "/app.js": ("app.js", "text/javascript; charset=utf-8"),
        }
        if route in static_routes:
            self._serve_static(*static_routes[route])
            return
        if route == "/api/status":
            status = self.chat_server.ollama.status()
            status.update(
                {
                    "defaultSystemPrompt": self.chat_server.config.system_prompt,
                    "limits": {
                        "messageChars": MAX_MESSAGE_CHARS,
                        "systemPromptChars": MAX_SYSTEM_PROMPT_CHARS,
                        "historyMessages": MAX_HISTORY_MESSAGES,
                        "historyChars": MAX_HISTORY_CHARS,
                    },
                }
            )
            self._send_json(HTTPStatus.OK, status)
            return
        self._send_error_json(HTTPStatus.NOT_FOUND, "Маршрут не найден")

    def _read_json_body(self) -> Any:
        content_type = self.headers.get("Content-Type", "")
        if content_type.split(";", 1)[0].strip().lower() != "application/json":
            raise RequestValidationError(
                "Content-Type должен быть application/json",
                HTTPStatus.UNSUPPORTED_MEDIA_TYPE,
            )
        if self.headers.get("Transfer-Encoding"):
            raise RequestValidationError(
                "Потоковое тело запроса не поддерживается", HTTPStatus.BAD_REQUEST
            )
        raw_length = self.headers.get("Content-Length")
        if raw_length is None:
            raise RequestValidationError(
                "Отсутствует Content-Length", HTTPStatus.LENGTH_REQUIRED
            )
        try:
            length = int(raw_length)
        except ValueError as exc:
            raise RequestValidationError("Некорректный Content-Length") from exc
        if length <= 0:
            raise RequestValidationError("Тело запроса пусто")
        if length > MAX_REQUEST_BYTES:
            raise RequestValidationError(
                f"Запрос больше {MAX_REQUEST_BYTES} байт",
                HTTPStatus.REQUEST_ENTITY_TOO_LARGE,
            )
        raw = self.rfile.read(length)
        if len(raw) != length:
            raise RequestValidationError("Тело запроса передано не полностью")
        try:
            text = raw.decode("utf-8")
        except UnicodeDecodeError as exc:
            raise RequestValidationError("JSON должен быть в кодировке UTF-8") from exc
        try:
            return json.loads(text)
        except json.JSONDecodeError as exc:
            raise RequestValidationError(f"Некорректный JSON: {exc.msg}") from exc

    def _write_stream_event(self, event: dict[str, Any]) -> None:
        self.wfile.write(_json_bytes(event) + b"\n")
        self.wfile.flush()

    def do_POST(self) -> None:
        if urlparse(self.path).path != "/api/chat":
            self._send_error_json(HTTPStatus.NOT_FOUND, "Маршрут не найден")
            return
        try:
            payload = self._read_json_body()
            messages, system_prompt = validate_chat_payload(
                payload, self.chat_server.config.system_prompt
            )
        except RequestValidationError as exc:
            self._send_error_json(exc.status, str(exc))
            return

        ollama_messages: list[dict[str, str]] = []
        if system_prompt.strip():
            ollama_messages.append({"role": "system", "content": system_prompt})
        ollama_messages.extend(messages)

        try:
            self.chat_server.ollama.ensure_model_available()
            stream = self.chat_server.ollama.open_chat_stream(ollama_messages)
        except ModelUnavailableError as exc:
            self._send_error_json(HTTPStatus.SERVICE_UNAVAILABLE, str(exc))
            return
        except OllamaError as exc:
            self._send_error_json(HTTPStatus.BAD_GATEWAY, str(exc))
            return

        self.send_response(HTTPStatus.OK)
        self.send_header("Content-Type", "application/x-ndjson; charset=utf-8")
        self.send_header("Cache-Control", "no-store, no-transform")
        self.send_header("Connection", "close")
        self.send_header("X-Accel-Buffering", "no")
        self._security_headers()
        self.end_headers()
        self.close_connection = True

        client_connected = True
        try:
            self._write_stream_event(
                {"type": "meta", "model": self.chat_server.config.model}
            )
            for event in stream:
                message = event.get("message")
                if isinstance(message, dict):
                    content = message.get("content")
                    if isinstance(content, str) and content:
                        self._write_stream_event(
                            {"type": "chunk", "content": content}
                        )
                if event.get("done") is True:
                    metrics = {
                        key: event[key]
                        for key in (
                            "total_duration",
                            "load_duration",
                            "prompt_eval_count",
                            "prompt_eval_duration",
                            "eval_count",
                            "eval_duration",
                        )
                        if key in event
                    }
                    self._write_stream_event(
                        {
                            "type": "done",
                            "doneReason": event.get("done_reason"),
                            "metrics": metrics,
                        }
                    )
        except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
            client_connected = False
            self.log_message("client disconnected during generation")
        except OllamaError as exc:
            if client_connected:
                try:
                    self._write_stream_event({"type": "error", "message": str(exc)})
                except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
                    pass
        finally:
            stream.close()


def create_server(
    host: str = DEFAULT_HOST,
    port: int = DEFAULT_PORT,
    *,
    model: str = DEFAULT_MODEL,
    system_prompt: str = DEFAULT_SYSTEM_PROMPT,
    ollama_url: str = DEFAULT_OLLAMA_URL,
    connect_timeout: float = CONNECT_TIMEOUT_SECONDS,
    read_timeout: float = READ_TIMEOUT_SECONDS,
    web_dir: Path = WEB_DIR,
) -> ChatHTTPServer:
    if host not in ("127.0.0.1", "localhost"):
        raise ValueError("Web server host must be 127.0.0.1 or localhost")
    if not (0 <= port <= 65535):
        raise ValueError("Web server port must be between 0 and 65535")
    if not model.strip():
        raise ValueError("Model name must not be empty")
    if len(system_prompt) > MAX_SYSTEM_PROMPT_CHARS:
        raise ValueError(
            f"Default system prompt exceeds {MAX_SYSTEM_PROMPT_CHARS} characters"
        )
    config = ServerConfig(
        model=model,
        system_prompt=system_prompt,
        ollama_url=_validate_ollama_url(ollama_url),
        connect_timeout=connect_timeout,
        read_timeout=read_timeout,
    )
    return ChatHTTPServer((host, port), config, web_dir)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Run the local Ollama web chat")
    parser.add_argument("--host", default=DEFAULT_HOST)
    parser.add_argument(
        "--port", type=int, default=int(os.environ.get("WEB_PORT", DEFAULT_PORT))
    )
    parser.add_argument(
        "--model", default=os.environ.get("OLLAMA_MODEL", DEFAULT_MODEL)
    )
    parser.add_argument(
        "--system-prompt",
        default=os.environ.get("OLLAMA_SYSTEM_PROMPT", DEFAULT_SYSTEM_PROMPT),
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        server = create_server(
            args.host,
            args.port,
            model=args.model,
            system_prompt=args.system_prompt,
        )
    except (OSError, ValueError) as exc:
        print(f"ERROR: cannot start web server: {exc}", file=sys.stderr)
        return 1

    host, port = server.server_address[:2]
    print("Day 26–27 — local Ollama web chat", flush=True)
    print(f"URL: http://{host}:{port}", flush=True)
    print(f"Ollama: {DEFAULT_OLLAMA_URL}", flush=True)
    print(f"Model: {server.config.model}", flush=True)
    print("Press Ctrl+C to stop.", flush=True)
    try:
        server.serve_forever(poll_interval=0.25)
    except KeyboardInterrupt:
        print("\nStopping web server...", flush=True)
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
