from __future__ import annotations

import http.client
import json
import sys
import threading
import time
import unittest
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any


DAY_DIR = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(DAY_DIR))

from web_server import MAX_MESSAGE_CHARS, create_server  # noqa: E402


def json_bytes(value: Any) -> bytes:
    return json.dumps(value, ensure_ascii=False).encode("utf-8")


class FakeOllamaHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    @property
    def fake_server(self) -> "FakeOllamaServer":
        return self.server  # type: ignore[return-value]

    def log_message(self, _format: str, *_args: Any) -> None:
        return

    def send_json(self, status: int, value: Any) -> None:
        body = json_bytes(value)
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:
        if self.path == "/api/version":
            self.send_json(HTTPStatus.OK, {"version": "test-1.0"})
            return
        if self.path == "/api/tags":
            self.send_json(
                HTTPStatus.OK,
                {"models": [{"name": name} for name in self.fake_server.models]},
            )
            return
        self.send_json(HTTPStatus.NOT_FOUND, {"error": "not found"})

    def do_POST(self) -> None:
        if self.path != "/api/chat":
            self.send_json(HTTPStatus.NOT_FOUND, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length", "0"))
        payload = json.loads(self.rfile.read(length))
        self.fake_server.chat_requests.append(payload)
        if self.fake_server.chat_status != HTTPStatus.OK:
            self.send_json(
                self.fake_server.chat_status,
                {"error": self.fake_server.chat_error},
            )
            return

        self.send_response(HTTPStatus.OK)
        self.send_header("Content-Type", "application/x-ndjson")
        self.send_header("Connection", "close")
        self.end_headers()
        self.close_connection = True
        for chunk in self.fake_server.chunks:
            line = json_bytes(
                {
                    "model": "qwen2.5:3b",
                    "message": {"role": "assistant", "content": chunk},
                    "done": False,
                }
            )
            self.wfile.write(line + b"\n")
            self.wfile.flush()
            if self.fake_server.chunk_delay:
                time.sleep(self.fake_server.chunk_delay)
        self.wfile.write(
            json_bytes(
                {
                    "model": "qwen2.5:3b",
                    "message": {"role": "assistant", "content": ""},
                    "done": True,
                    "done_reason": "stop",
                    "prompt_eval_count": 20,
                    "eval_count": 4,
                }
            )
            + b"\n"
        )
        self.wfile.flush()


class FakeOllamaServer(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self) -> None:
        super().__init__(("127.0.0.1", 0), FakeOllamaHandler)
        self.models = ["qwen2.5:3b"]
        self.chunks = ["Привет", " из Ollama"]
        self.chunk_delay = 0.0
        self.chat_status = HTTPStatus.OK
        self.chat_error = "fake Ollama failure"
        self.chat_requests: list[dict[str, Any]] = []


class WebServerTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.fake = FakeOllamaServer()
        fake_port = cls.fake.server_address[1]
        cls.fake_thread = threading.Thread(target=cls.fake.serve_forever, daemon=True)
        cls.fake_thread.start()

        cls.web = create_server(
            "127.0.0.1",
            0,
            ollama_url=f"http://127.0.0.1:{fake_port}",
            connect_timeout=1,
            read_timeout=3,
        )
        cls.web_port = cls.web.server_address[1]
        cls.web_thread = threading.Thread(target=cls.web.serve_forever, daemon=True)
        cls.web_thread.start()

    @classmethod
    def tearDownClass(cls) -> None:
        cls.web.shutdown()
        cls.web.server_close()
        cls.fake.shutdown()
        cls.fake.server_close()
        cls.web_thread.join(timeout=2)
        cls.fake_thread.join(timeout=2)

    def setUp(self) -> None:
        self.fake.models = ["qwen2.5:3b"]
        self.fake.chunks = ["Привет", " из Ollama"]
        self.fake.chunk_delay = 0.0
        self.fake.chat_status = HTTPStatus.OK
        self.fake.chat_error = "fake Ollama failure"
        self.fake.chat_requests.clear()

    def request(
        self,
        method: str,
        path: str,
        body: bytes | None = None,
        content_type: str = "application/json",
    ) -> tuple[int, dict[str, str], bytes]:
        connection = http.client.HTTPConnection("127.0.0.1", self.web_port, timeout=5)
        headers = {"Content-Type": content_type} if body is not None else {}
        connection.request(method, path, body=body, headers=headers)
        response = connection.getresponse()
        raw = response.read()
        result = (response.status, dict(response.getheaders()), raw)
        connection.close()
        return result

    def post_chat(self, payload: Any) -> tuple[int, dict[str, str], bytes]:
        return self.request("POST", "/api/chat", json_bytes(payload))

    def test_successful_multiturn_dialog_forwards_history_and_system_prompt(self) -> None:
        payload = {
            "systemPrompt": "Отвечай кратко.",
            "messages": [
                {"role": "user", "content": "Запомни число 17."},
                {"role": "assistant", "content": "Запомнил."},
                {"role": "user", "content": "Какое число?"},
            ],
        }
        status, headers, raw = self.post_chat(payload)

        self.assertEqual(status, HTTPStatus.OK)
        self.assertIn("application/x-ndjson", headers["Content-Type"])
        events = [json.loads(line) for line in raw.splitlines()]
        self.assertEqual(
            "".join(event.get("content", "") for event in events),
            "Привет из Ollama",
        )
        self.assertEqual(events[-1]["type"], "done")
        self.assertEqual(len(self.fake.chat_requests), 1)
        forwarded = self.fake.chat_requests[0]
        self.assertTrue(forwarded["stream"])
        self.assertEqual(forwarded["model"], "qwen2.5:3b")
        self.assertEqual(
            forwarded["messages"],
            [
                {"role": "system", "content": "Отвечай кратко."},
                *payload["messages"],
            ],
        )

    def test_streaming_is_forwarded_as_separate_events(self) -> None:
        self.fake.chunks = ["первый ", "второй ", "третий"]
        self.fake.chunk_delay = 0.02
        connection = http.client.HTTPConnection("127.0.0.1", self.web_port, timeout=5)
        body = json_bytes(
            {"messages": [{"role": "user", "content": "Считай до трёх"}]}
        )
        connection.request(
            "POST",
            "/api/chat",
            body=body,
            headers={"Content-Type": "application/json"},
        )
        response = connection.getresponse()
        self.assertEqual(response.status, HTTPStatus.OK)
        events = []
        while True:
            line = response.readline()
            if not line:
                break
            events.append(json.loads(line))
        connection.close()

        chunk_events = [event for event in events if event["type"] == "chunk"]
        self.assertEqual(
            [event["content"] for event in chunk_events], self.fake.chunks
        )
        self.assertEqual(events[0]["type"], "meta")
        self.assertEqual(events[-1]["type"], "done")

    def test_invalid_json_is_rejected(self) -> None:
        status, _, raw = self.request("POST", "/api/chat", b"{broken")
        self.assertEqual(status, HTTPStatus.BAD_REQUEST)
        self.assertIn("Некорректный JSON", json.loads(raw)["error"])
        self.assertEqual(self.fake.chat_requests, [])

    def test_empty_and_too_long_messages_are_rejected(self) -> None:
        cases = (
            (
                {"messages": [{"role": "user", "content": "   "}]},
                HTTPStatus.BAD_REQUEST,
                "пусто",
            ),
            (
                {
                    "messages": [
                        {"role": "user", "content": "x" * (MAX_MESSAGE_CHARS + 1)}
                    ]
                },
                HTTPStatus.REQUEST_ENTITY_TOO_LARGE,
                "длиннее",
            ),
        )
        for payload, expected_status, expected_text in cases:
            with self.subTest(expected_status=expected_status):
                status, _, raw = self.post_chat(payload)
                self.assertEqual(status, expected_status)
                self.assertIn(expected_text, json.loads(raw)["error"])
        self.assertEqual(self.fake.chat_requests, [])

    def test_ollama_error_becomes_bad_gateway(self) -> None:
        self.fake.chat_status = HTTPStatus.INTERNAL_SERVER_ERROR
        self.fake.chat_error = "generation exploded"
        status, _, raw = self.post_chat(
            {"messages": [{"role": "user", "content": "Привет"}]}
        )
        self.assertEqual(status, HTTPStatus.BAD_GATEWAY)
        self.assertIn("generation exploded", json.loads(raw)["error"])

    def test_unavailable_model_is_reported_before_generation(self) -> None:
        self.fake.models = ["another-model:latest"]
        status, _, raw = self.post_chat(
            {"messages": [{"role": "user", "content": "Привет"}]}
        )
        self.assertEqual(status, HTTPStatus.SERVICE_UNAVAILABLE)
        self.assertIn("не установлена", json.loads(raw)["error"])
        self.assertEqual(self.fake.chat_requests, [])


if __name__ == "__main__":
    unittest.main(verbosity=2)
