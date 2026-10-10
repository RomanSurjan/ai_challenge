#!/usr/bin/env python3
"""Local, dependency-free web chat proxy for Ollama.

The browser only talks to this server. The server validates the complete chat
history, prepends the configured system prompt, and streams normalized NDJSON
events from the local Ollama /api/chat endpoint.
"""

from __future__ import annotations

import argparse
import hmac
import http.client
import math
import select
import threading
import time
from collections import deque
from contextlib import contextmanager
import json
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
DEFAULT_PORT = 8030
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
    proxy_secret: str = ""
    allowed_origins: tuple[str, ...] = ()
    num_ctx: int = 8192
    num_predict: int = 512
    rate_per_minute: float = 10
    burst: int = 3
    queue_size: int = 3
    queue_timeout: float = 30
    request_timeout: float = 10
    max_connections: int = 32


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
                except (json.JSONDecodeError, UnicodeDecodeError) as exc:
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

    def verify_template(self) -> None:
        request = urllib.request.Request(
            f"{self.base_url}/api/show", data=_json_bytes({"model": self.config.model}),
            headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(request, timeout=self.config.connect_timeout) as response:
                raw = response.read(MAX_OLLAMA_RESPONSE_BYTES + 1)
            if len(raw) > MAX_OLLAMA_RESPONSE_BYTES:
                raise ValueError("metadata too large")
            meta = json.loads(raw)
            if not isinstance(meta, dict):
                raise ValueError("invalid metadata")
            model_info = meta.get("model_info", {})
            if (model_info.get("tokenizer.ggml.model") != "gpt2" or
                    model_info.get("tokenizer.ggml.pre") != "qwen2" or
                    meta.get("messages") or
                    meta.get("details", {}).get("family") != "qwen2" or
                    meta.get("template") != (ROOT_DIR / "model_template.txt").read_text()):
                raise ValueError("model template changed")
        except (ValueError, TypeError, AttributeError, OSError, urllib.error.URLError) as exc:
            raise OllamaError("Не подтверждён шаблон Qwen2.5; генерация запрещена") from exc

    def open_chat_stream(self, messages: list[dict[str, str]], lease: "GenerationLease") -> OllamaStream:
        parsed = urlparse(self.base_url)
        conn = http.client.HTTPConnection(parsed.hostname, parsed.port or 80,
                                         timeout=self.config.connect_timeout)
        lease.attach(conn)
        payload = {"model": self.config.model, "messages": messages, "stream": True,
                   "keep_alive": "10m", "options": {"num_ctx": self.config.num_ctx,
                   "num_predict": self.config.num_predict, "temperature": 0, "seed": 0}}
        try:
            conn.connect()
            if lease.stopped.is_set():
                lease.abort()
                raise OllamaError("Генерация отменена")
            lease.upstream_socket = conn.sock
            conn.sock.settimeout(self.config.read_timeout)
            conn.request("POST", "/api/chat", body=_json_bytes(payload),
                         headers={"Content-Type": "application/json"})
            response = conn.getresponse()
            if response.status != 200:
                detail = _safe_error_detail(response.read(4096))
                response.close()
                raise OllamaError(f"Ollama вернул HTTP {response.status}: {detail}")
            return OllamaStream(response)
        except (OSError, http.client.HTTPException) as exc:
            raise OllamaError("Не удалось начать генерацию через Ollama") from exc


def context_bound(messages: list[dict[str, str]]) -> int:
    """Qwen byte-level BPE upper bound, including every ChatML turn.

    Initial byte symbols <= UTF-8 bytes; BPE merges cannot increase their count.
    Count literal framing bytes too (special tokens can only lower this bound).
    256 extra tokens cover BOS/EOS/runtime framing conservatively. No tools.
    The installed Ollama template is verified before every generation.
    """
    framed = "".join(f"<|im_start|>{m['role']}\n{m['content']}<|im_end|>\n"
                     for m in messages) + "<|im_start|>assistant\n"
    return len(framed.encode("utf-8")) + 256


class RateLimiter:
    def __init__(self, per_minute: float, burst: int, clock=time.monotonic):
        self.rate, self.burst, self.clock = per_minute / 60, burst, clock
        self.lock = threading.Lock()
        self.buckets: dict[str, tuple[float, float]] = {}

    def check(self, user: str) -> int:
        with self.lock:
            now = self.clock()
            tokens, updated = self.buckets.get(user, (self.burst, now))
            tokens = min(self.burst, tokens + (now - updated) * self.rate)
            if tokens < 1:
                self.buckets[user] = (tokens, now)
                return max(1, math.ceil((1 - tokens) / self.rate))
            self.buckets[user] = (tokens - 1, now)
            return 0


class GenerationQueue:
    """One active generation and a bounded FIFO queue; waits have deadlines."""
    def __init__(self, size: int, timeout: float):
        self.size, self.timeout = size, timeout
        self.condition = threading.Condition()
        self.active = 0
        self.waiters: deque[object] = deque()

    @contextmanager
    def slot(self, disconnected=lambda: False):
        ticket = object()
        acquired = False
        with self.condition:
            if (self.active or self.waiters) and len(self.waiters) >= self.size:
                raise RequestValidationError("Очередь генерации заполнена", 503)
            self.waiters.append(ticket)
            deadline = time.monotonic() + self.timeout
            try:
                while self.active or self.waiters[0] is not ticket:
                    if disconnected():
                        raise RequestValidationError("Запрос отменён в очереди", 499)
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        raise RequestValidationError("Время ожидания очереди превышено", 503)
                    self.condition.wait(min(remaining, .1))
                if disconnected():
                    raise RequestValidationError("Запрос отменён", 499)
                self.waiters.popleft()
                self.active = 1
                acquired = True
            finally:
                if not acquired:
                    self.waiters.remove(ticket)
                    self.condition.notify_all()
        try:
            yield
        finally:
            with self.condition:
                self.active = 0
                self.condition.notify_all()

    def status(self):
        with self.condition:
            return {"active": self.active, "queued": len(self.waiters),
                    "capacity": self.size}


def peer_disconnected(sock: socket.socket) -> bool:
    try:
        readable, _, _ = select.select([sock], [], [], 0)
        return bool(readable) and not sock.recv(1, socket.MSG_PEEK)
    except OSError:
        return True


class GenerationLease:
    """Interrupt blocked upstream reads on disconnect or absolute deadline."""
    def __init__(self, peer: socket.socket, timeout: float):
        self.peer, self.deadline = peer, time.monotonic() + timeout
        self.stopped = threading.Event()
        self.finished = threading.Event()
        self.conn = None
        self.upstream_socket = None
        self.reason = ""
        self.thread = threading.Thread(target=self._watch, daemon=True)

    def attach(self, conn):
        self.conn = conn

    def abort(self):
        self.stopped.set()
        conn = self.conn
        sock = self.upstream_socket or (conn.sock if conn else None)
        if sock:
            try:
                sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
        # response.close() happens in the handler, never races its readline here.

    def _watch(self):
        while not self.finished.wait(.05):
            if peer_disconnected(self.peer):
                self.reason = "Запрос отменён клиентом"
            elif time.monotonic() >= self.deadline:
                self.reason = "Превышено время генерации"
            else:
                continue
            self.abort()
            return

    def __enter__(self):
        self.thread.start()
        return self

    def __exit__(self, *_):
        self.finished.set()
        self.thread.join(timeout=1)
        if self.conn:
            self.conn.close()


def validate_chat_payload(
    payload: Any, default_system_prompt: str
) -> tuple[list[dict[str, str]], str]:
    if not isinstance(payload, dict):
        raise RequestValidationError("Корневое значение JSON должно быть объектом")

    if set(payload) - {"messages", "systemPrompt"}:
        raise RequestValidationError("Модель и параметры задаются сервером; неизвестные поля запрещены")
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
        try:
            content.encode("utf-8")
        except UnicodeEncodeError as exc:
            raise RequestValidationError("Сообщение содержит некорректный Unicode") from exc
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
    try:
        system_prompt.encode("utf-8")
    except UnicodeEncodeError as exc:
        raise RequestValidationError("Системный промт содержит некорректный Unicode") from exc
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
        self.limiter = RateLimiter(config.rate_per_minute, config.burst)
        self.queue = GenerationQueue(config.queue_size, config.queue_timeout)
        self.connections = threading.BoundedSemaphore(config.max_connections)
        self.ollama = OllamaClient(config)
        self.web_dir = web_dir.resolve()
        super().__init__(server_address, ChatRequestHandler)

    def handle_error(self, request, client_address):
        # Operational failures are surfaced via API; do not log request data.
        print("Backend connection handler failed", file=sys.stderr)

    def process_request(self, request, client_address):
        if not self.connections.acquire(blocking=False):
            request.close()
            return
        try:
            super().process_request(request, client_address)
        except BaseException:
            self.connections.release()
            raise

    def process_request_thread(self, request, client_address):
        try:
            super().process_request_thread(request, client_address)
        finally:
            self.connections.release()


class ChatRequestHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "Day30PrivateChat/1.0"

    @property
    def chat_server(self) -> ChatHTTPServer:
        return self.server  # type: ignore[return-value]

    def setup(self):
        super().setup()
        self.connection.settimeout(self.chat_server.config.request_timeout)

    def log_message(self, format_string: str, *args: Any) -> None:
        # Never persist paths, headers, credentials or chat content.
        return

    def _authorize(self) -> bool:
        supplied = self.headers.get("X-Private-Token", "")
        user = self.headers.get("X-Authenticated-User", "")
        if (not supplied.isascii() or not hmac.compare_digest(supplied, self.chat_server.config.proxy_secret)
                or not user or len(user) > 128 or not user.isascii()):
            self._send_error_json(403, "Доступ только через защищённый HTTPS-вход")
            return False
        self.auth_user = user
        return True

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
        if not self._authorize():
            return
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
        if route == "/health/live":
            self._send_json(200, {"live": True})
            return
        if route == "/health/ready":
            status = self.chat_server.ollama.status()
            ready = status["connected"] and status["modelAvailable"]
            if ready:
                try:
                    self.chat_server.ollama.verify_template()
                except OllamaError:
                    ready = False
            self._send_json(200 if ready else 503, {"ready": ready,
                "model": self.chat_server.config.model, "method": "installed_model_and_verified_template"})
            return
        if route == "/api/status":
            status = self.chat_server.ollama.status()
            status.update(
                {
                    "generation": {"num_ctx": self.chat_server.config.num_ctx,
                        "num_predict": self.chat_server.config.num_predict,
                        "contextMethod": "utf8_byte_bpe_upper_bound_multiturn_chatml",
                        "ratePerMinute": self.chat_server.config.rate_per_minute,
                        "burst": self.chat_server.config.burst},
                    "queue": self.chat_server.queue.status(),
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
        # Absolute timer prevents slow-drip clients resetting a socket timeout.
        def expire_body():
            try:
                self.connection.shutdown(socket.SHUT_RD)
            except OSError:
                pass
        timer = threading.Timer(self.chat_server.config.request_timeout, expire_body)
        timer.start()
        try:
            raw = self.rfile.read(length)
        except (OSError, TimeoutError) as exc:
            raise RequestValidationError("Время чтения запроса превышено", 408) from exc
        finally:
            timer.cancel()
        if len(raw) != length:
            raise RequestValidationError("Тело запроса передано не полностью или истёк срок чтения", 408)
        try:
            text = raw.decode("utf-8")
        except UnicodeDecodeError as exc:
            raise RequestValidationError("JSON должен быть в кодировке UTF-8") from exc
        try:
            return json.loads(text)
        except json.JSONDecodeError as exc:
            raise RequestValidationError(f"Некорректный JSON: {exc.msg}") from exc

    def _write_stream_event(self, event: dict[str, Any]) -> None:
        try:
            body = _json_bytes(event)
        except UnicodeEncodeError as exc:
            raise OllamaError("Ollama вернул некорректный Unicode") from exc
        self.wfile.write(body + b"\n")
        self.wfile.flush()

    def do_POST(self) -> None:
        if not self._authorize():
            return
        if urlparse(self.path).path != "/api/chat":
            self._send_error_json(404, "Маршрут не найден")
            return
        origin = self.headers.get("Origin")
        if origin is not None and origin not in self.chat_server.config.allowed_origins:
            self._send_error_json(403, "Origin не разрешён")
            return
        try:
            payload = self._read_json_body()
            messages, system_prompt = validate_chat_payload(payload, self.chat_server.config.system_prompt)
            # Even an empty system message overrides Ollama's factory system.
            # /api/chat v0.35.0 inserts that default only if first role != system.
            ollama_messages = [{"role": "system", "content": system_prompt}] + messages
            bound = context_bound(ollama_messages)
            config = self.chat_server.config
            if bound + config.num_predict > config.num_ctx:
                raise RequestValidationError(
                    f"Контекст превышен: верхняя оценка UTF-8/BPE {bound} + резерв ответа "
                    f"{config.num_predict} > num_ctx {config.num_ctx}. История не обрезана.", 413)
            retry = self.chat_server.limiter.check(self.auth_user)
            if retry:
                self.send_response(429)
                self.send_header("Retry-After", str(retry))
                body = _json_bytes({"error": "Превышен rate limit", "retryAfter": retry})
                self.send_header("Content-Type", "application/json; charset=utf-8")
                self.send_header("Content-Length", str(len(body)))
                self.send_header("Connection", "close")
                self._security_headers()
                self.end_headers()
                self.close_connection = True
                self.wfile.write(body)
                return
            with self.chat_server.queue.slot(lambda: peer_disconnected(self.connection)):
                self._generate(ollama_messages, bound)
        except RequestValidationError as exc:
            self._send_error_json(exc.status, str(exc))
        except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
            self.close_connection = True

    def _generate(self, messages, bound):
        stream = None
        started = False
        with GenerationLease(self.connection, self.chat_server.config.read_timeout) as lease:
            try:
                self.chat_server.ollama.ensure_model_available()
                self.chat_server.ollama.verify_template()
                stream = self.chat_server.ollama.open_chat_stream(messages, lease)
                if lease.stopped.is_set():
                    raise OllamaError(lease.reason)
                self.send_response(200)
                self.send_header("Content-Type", "application/x-ndjson; charset=utf-8")
                self.send_header("Cache-Control", "no-store, no-transform")
                self.send_header("Connection", "close")
                self.send_header("X-Accel-Buffering", "no")
                self._security_headers()
                self.end_headers()
                started = True
                self.close_connection = True
                self._write_stream_event({"type": "meta", "model": self.chat_server.config.model,
                                          "inputUpperBound": bound,
                                          "contextMethod": "utf8_byte_bpe_upper_bound_multiturn_chatml"})
                for event in stream:
                    if lease.stopped.is_set():
                        raise OllamaError(lease.reason)
                    message = event.get("message")
                    if isinstance(message, dict):
                        content = message.get("content")
                        if isinstance(content, str) and content:
                            self._write_stream_event({"type": "chunk", "content": content})
                    if event.get("done") is True:
                        metrics = {key: event[key] for key in ("total_duration", "load_duration",
                                   "prompt_eval_count", "prompt_eval_duration", "eval_count",
                                   "eval_duration") if key in event}
                        self._write_stream_event({"type": "done", "doneReason": event.get("done_reason"),
                                                  "metrics": metrics})
            except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
                lease.abort()
            except (OllamaError, http.client.HTTPException, OSError) as exc:
                message = lease.reason or str(exc)
                if started:
                    try:
                        self._write_stream_event({"type": "error", "message": message})
                    except OSError:
                        pass
                else:
                    status = 504 if lease.reason else (503 if isinstance(exc, ModelUnavailableError) else 502)
                    self._send_error_json(status, message)
            finally:
                if stream:
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
    **settings,
) -> ChatHTTPServer:
    if host not in ("127.0.0.1", "localhost"):
        raise ValueError("Web server host must be 127.0.0.1 or localhost")
    if not (0 <= port <= 65535):
        raise ValueError("Web server port must be between 0 and 65535")
    if model != DEFAULT_MODEL:
        raise ValueError("Only verified qwen2.5:3b is supported")
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
        **settings,
    )
    if len(config.proxy_secret) < 32 or not config.proxy_secret.isascii():
        raise ValueError("A proxy secret of at least 32 ASCII characters is required")
    if not (512 <= config.num_ctx <= 32768 and 1 <= config.num_predict < config.num_ctx):
        raise ValueError("Invalid context/output limits")
    if (config.rate_per_minute <= 0 or config.burst < 1 or config.queue_size < 0 or
            min(config.queue_timeout, config.request_timeout, config.read_timeout, config.connect_timeout) <= 0 or
            config.max_connections < 1):
        raise ValueError("Limits must be positive (queue size may be zero)")
    return ChatHTTPServer((host, port), config, web_dir)


def main() -> int:
    parser = argparse.ArgumentParser(description="Private Day 30 Ollama service")
    parser.add_argument("--config", required=True, type=Path)
    args = parser.parse_args()
    try:
        settings = json.loads(args.config.read_text())
        server = create_server(**settings)
    except (OSError, ValueError, TypeError):
        print("Cannot start backend: check private config and port", file=sys.stderr)
        return 1
    print("Day 30 backend started on loopback", flush=True)
    try:
        server.serve_forever(poll_interval=.1)
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
