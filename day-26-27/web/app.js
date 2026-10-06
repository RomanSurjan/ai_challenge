"use strict";

const STORAGE_KEY = "day26-27-local-chat-v1";
const LEGACY_STORAGE_KEY = "day26-local-chat-v1";
const FALLBACK_SYSTEM_PROMPT =
  "Ты полезный локальный ассистент. Отвечай точно, ясно и на языке пользователя.";

const elements = {
  form: document.querySelector("#chat-form"),
  input: document.querySelector("#message-input"),
  messages: document.querySelector("#messages"),
  emptyState: document.querySelector("#empty-state"),
  sendButton: document.querySelector("#send-button"),
  stopButton: document.querySelector("#stop-button"),
  newChatButton: document.querySelector("#new-chat-button"),
  generationStatus: document.querySelector("#generation-status"),
  statusDot: document.querySelector("#status-dot"),
  statusText: document.querySelector("#status-text"),
  connectionDetail: document.querySelector("#connection-detail"),
  modelName: document.querySelector("#model-name"),
  systemPrompt: document.querySelector("#system-prompt"),
  systemCount: document.querySelector("#system-count"),
  errorBanner: document.querySelector("#error-banner"),
  errorText: document.querySelector("#error-text"),
  errorClose: document.querySelector("#error-close"),
  chatPanel: document.querySelector(".chat-panel"),
  composerWrap: document.querySelector(".composer-wrap"),
};

const state = {
  messages: [],
  systemPrompt: "",
  model: "qwen2.5:3b",
  maxMessageChars: 12000,
  maxSystemPromptChars: 4000,
  hasStoredSystemPrompt: false,
  generating: false,
  controller: null,
};

function isStoredMessage(value) {
  return (
    value &&
    typeof value === "object" &&
    (value.role === "user" || value.role === "assistant") &&
    typeof value.content === "string" &&
    value.content.trim().length > 0 &&
    value.content.length <= state.maxMessageChars
  );
}

function loadStoredChat() {
  try {
    const raw =
      localStorage.getItem(STORAGE_KEY) ?? localStorage.getItem(LEGACY_STORAGE_KEY);
    if (!raw) return;
    const saved = JSON.parse(raw);
    if (!saved || saved.version !== 1) return;
    if (Array.isArray(saved.messages)) {
      const restored = saved.messages.filter(isStoredMessage).slice(-64);
      const alternating = [];
      let expectedRole = "user";
      for (const message of restored) {
        if (message.role !== expectedRole) break;
        alternating.push({ role: message.role, content: message.content });
        expectedRole = expectedRole === "user" ? "assistant" : "user";
      }
      state.messages = alternating;
    }
    if (typeof saved.systemPrompt === "string") {
      state.systemPrompt = saved.systemPrompt.slice(0, state.maxSystemPromptChars);
      state.hasStoredSystemPrompt = true;
    }
  } catch (error) {
    console.warn("Не удалось восстановить локальный диалог", error);
  }
}

function saveChat() {
  try {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        version: 1,
        model: state.model,
        systemPrompt: state.systemPrompt,
        messages: state.messages,
      }),
    );
    localStorage.removeItem(LEGACY_STORAGE_KEY);
  } catch (error) {
    console.warn("Не удалось сохранить локальный диалог", error);
  }
}

function appendTextPart(container, text) {
  if (!text) return;
  const block = document.createElement("div");
  block.className = "rendered-text";
  block.textContent = text;
  container.append(block);
}

function appendCodePart(container, language, code) {
  const wrapper = document.createElement("div");
  wrapper.className = "code-block";
  if (language) {
    const label = document.createElement("div");
    label.className = "code-language";
    label.textContent = language;
    wrapper.append(label);
  }
  const pre = document.createElement("pre");
  const codeElement = document.createElement("code");
  codeElement.textContent = code;
  pre.append(codeElement);
  wrapper.append(pre);
  container.append(wrapper);
}

function renderSafeContent(container, text) {
  container.replaceChildren();
  let cursor = 0;
  while (cursor < text.length) {
    const fenceStart = text.indexOf("```", cursor);
    if (fenceStart < 0) {
      appendTextPart(container, text.slice(cursor));
      break;
    }
    appendTextPart(container, text.slice(cursor, fenceStart));
    const firstLineEnd = text.indexOf("\n", fenceStart + 3);
    if (firstLineEnd < 0) {
      appendTextPart(container, text.slice(fenceStart));
      break;
    }
    const language = text.slice(fenceStart + 3, firstLineEnd).trim().slice(0, 40);
    const fenceEnd = text.indexOf("```", firstLineEnd + 1);
    if (fenceEnd < 0) {
      appendCodePart(container, language, text.slice(firstLineEnd + 1));
      break;
    }
    appendCodePart(container, language, text.slice(firstLineEnd + 1, fenceEnd));
    cursor = fenceEnd + 3;
  }
  if (!text) {
    const waiting = document.createElement("div");
    waiting.className = "rendered-text";
    waiting.textContent = "";
    container.append(waiting);
  }
}

function createMessageElement(message, index) {
  const article = document.createElement("article");
  article.className = `message ${message.role}`;
  article.dataset.index = String(index);

  const avatar = document.createElement("div");
  avatar.className = "avatar";
  avatar.setAttribute("aria-hidden", "true");
  avatar.textContent = message.role === "user" ? "ВЫ" : "Q";

  const content = document.createElement("div");
  content.className = "message-content";

  const label = document.createElement("div");
  label.className = "message-label";
  label.textContent = message.role === "user" ? "Вы" : state.model;

  const body = document.createElement("div");
  body.className = "message-body";
  if (state.generating && index === state.messages.length - 1 && message.role === "assistant") {
    body.classList.add("stream-caret");
  }
  renderSafeContent(body, message.content);

  content.append(label, body);
  article.append(avatar, content);
  return article;
}

function renderMessages() {
  const fragment = document.createDocumentFragment();
  if (state.messages.length === 0) {
    elements.emptyState.hidden = false;
    fragment.append(elements.emptyState);
  } else {
    elements.emptyState.hidden = true;
    for (let index = 0; index < state.messages.length; index += 1) {
      fragment.append(createMessageElement(state.messages[index], index));
    }
  }
  elements.messages.replaceChildren(fragment);
  scrollToLatest();
}

function updateLastAssistantMessage() {
  const index = state.messages.length - 1;
  const article = elements.messages.querySelector(`[data-index="${index}"]`);
  const body = article?.querySelector(".message-body");
  if (!body) {
    renderMessages();
    return;
  }
  renderSafeContent(body, state.messages[index].content);
  body.classList.toggle("stream-caret", state.generating);
  scrollToLatest();
}

function scrollToLatest() {
  requestAnimationFrame(() => {
    elements.messages.scrollTop = elements.messages.scrollHeight;
  });
}

function showError(message) {
  elements.errorText.textContent = message;
  elements.errorBanner.hidden = false;
}

function hideError() {
  elements.errorBanner.hidden = true;
  elements.errorText.textContent = "";
}

function updateSystemCount() {
  elements.systemCount.textContent =
    `${elements.systemPrompt.value.length} / ${state.maxSystemPromptChars}`;
}

function updateComposerState() {
  const hasText = elements.input.value.trim().length > 0;
  elements.sendButton.disabled = state.generating || !hasText;
  elements.stopButton.disabled = !state.generating;
  elements.generationStatus.hidden = !state.generating;
  requestAnimationFrame(updateComposerClearance);
}

function resizeInput() {
  elements.input.style.height = "auto";
  elements.input.style.height = `${Math.min(elements.input.scrollHeight, 190)}px`;
  requestAnimationFrame(updateComposerClearance);
}

function updateComposerClearance() {
  const height = Math.ceil(elements.composerWrap.getBoundingClientRect().height);
  if (height > 0) {
    elements.chatPanel.style.setProperty("--composer-clearance", `${height}px`);
  }
}

function setConnectionState(kind, text, detail) {
  elements.statusDot.className = `status-dot ${kind}`;
  elements.statusText.textContent = text;
  elements.connectionDetail.textContent = detail;
}

async function refreshStatus() {
  setConnectionState("checking", "Проверяем Ollama…", "127.0.0.1:11434");
  try {
    const response = await fetch("/api/status", {
      headers: { Accept: "application/json" },
      cache: "no-store",
    });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const status = await response.json();
    if (typeof status.model === "string") {
      state.model = status.model;
      elements.modelName.textContent = status.model;
    }
    if (status.limits && Number.isInteger(status.limits.messageChars)) {
      state.maxMessageChars = status.limits.messageChars;
      elements.input.maxLength = state.maxMessageChars;
    }
    if (status.limits && Number.isInteger(status.limits.systemPromptChars)) {
      state.maxSystemPromptChars = status.limits.systemPromptChars;
      elements.systemPrompt.maxLength = state.maxSystemPromptChars;
    }
    if (!state.hasStoredSystemPrompt && typeof status.defaultSystemPrompt === "string") {
      state.systemPrompt = status.defaultSystemPrompt;
      state.hasStoredSystemPrompt = true;
      elements.systemPrompt.value = state.systemPrompt;
      saveChat();
    }
    updateSystemCount();
    if (status.connected && status.modelAvailable) {
      const version = status.version ? `Ollama ${status.version}` : "127.0.0.1:11434";
      setConnectionState("online", "Готово к работе", version);
    } else if (status.connected) {
      setConnectionState("offline", "Модель недоступна", status.error || state.model);
    } else {
      setConnectionState("offline", "Ollama не отвечает", status.error || "127.0.0.1:11434");
    }
  } catch (error) {
    setConnectionState("offline", "Сервер недоступен", String(error));
  }
}

async function responseErrorMessage(response) {
  try {
    const payload = await response.json();
    if (payload && typeof payload.error === "string") return payload.error;
  } catch (_error) {
    // The fallback below is safer than exposing an arbitrary response body.
  }
  return `Сервер вернул HTTP ${response.status}`;
}

function processStreamLine(line) {
  if (!line.trim()) return false;
  let event;
  try {
    event = JSON.parse(line);
  } catch (_error) {
    throw new Error("Сервер вернул некорректное событие потока");
  }
  if (!event || typeof event.type !== "string") {
    throw new Error("Сервер вернул событие неизвестного формата");
  }
  if (event.type === "meta" && typeof event.model === "string") {
    state.model = event.model;
    elements.modelName.textContent = event.model;
    return false;
  }
  if (event.type === "chunk" && typeof event.content === "string") {
    state.messages[state.messages.length - 1].content += event.content;
    updateLastAssistantMessage();
    saveChat();
    return false;
  }
  if (event.type === "error") {
    throw new Error(typeof event.message === "string" ? event.message : "Ошибка генерации");
  }
  return event.type === "done";
}

async function streamReply(requestMessages) {
  state.controller = new AbortController();
  const response = await fetch("/api/chat", {
    method: "POST",
    headers: {
      Accept: "application/x-ndjson",
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      messages: requestMessages,
      systemPrompt: state.systemPrompt,
    }),
    signal: state.controller.signal,
  });
  if (!response.ok) {
    throw new Error(await responseErrorMessage(response));
  }
  if (!response.body) throw new Error("Браузер не получил поток ответа");

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let doneEventReceived = false;
  while (true) {
    const result = await reader.read();
    buffer += decoder.decode(result.value || new Uint8Array(), { stream: !result.done });
    let newline = buffer.indexOf("\n");
    while (newline >= 0) {
      const line = buffer.slice(0, newline);
      buffer = buffer.slice(newline + 1);
      doneEventReceived = processStreamLine(line) || doneEventReceived;
      newline = buffer.indexOf("\n");
    }
    if (result.done) break;
  }
  if (buffer.trim()) {
    doneEventReceived = processStreamLine(buffer) || doneEventReceived;
  }
  if (!doneEventReceived) throw new Error("Поток завершился раньше ответа модели");
}

async function sendMessage() {
  const content = elements.input.value.trim();
  if (!content || state.generating) return;
  if (content.length > state.maxMessageChars) {
    showError(`Сообщение длиннее ${state.maxMessageChars} символов.`);
    return;
  }

  hideError();
  state.messages.push({ role: "user", content });
  const requestMessages = state.messages.map((message) => ({ ...message }));
  state.messages.push({ role: "assistant", content: "" });
  state.generating = true;
  elements.input.value = "";
  resizeInput();
  updateComposerState();
  renderMessages();
  saveChat();

  try {
    await streamReply(requestMessages);
  } catch (error) {
    const aborted = error instanceof DOMException && error.name === "AbortError";
    const assistant = state.messages[state.messages.length - 1];
    if (assistant?.role === "assistant" && assistant.content.length === 0) {
      state.messages.pop();
      const failedUserMessage = state.messages[state.messages.length - 1];
      if (failedUserMessage?.role === "user") {
        state.messages.pop();
        elements.input.value = failedUserMessage.content;
        resizeInput();
      }
    }
    if (!aborted) {
      showError(error instanceof Error ? error.message : String(error));
      await refreshStatus();
    }
  } finally {
    state.generating = false;
    state.controller = null;
    updateComposerState();
    renderMessages();
    saveChat();
    elements.input.focus();
  }
}

function stopGeneration() {
  if (state.controller) state.controller.abort();
}

function startNewChat() {
  stopGeneration();
  state.messages = [];
  state.generating = false;
  hideError();
  saveChat();
  renderMessages();
  updateComposerState();
  elements.input.focus();
}

elements.form.addEventListener("submit", (event) => {
  event.preventDefault();
  void sendMessage();
});

elements.input.addEventListener("input", () => {
  resizeInput();
  updateComposerState();
});

elements.input.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
    event.preventDefault();
    void sendMessage();
  }
});

elements.systemPrompt.addEventListener("input", () => {
  state.systemPrompt = elements.systemPrompt.value;
  state.hasStoredSystemPrompt = true;
  updateSystemCount();
  saveChat();
});

elements.stopButton.addEventListener("click", stopGeneration);
elements.newChatButton.addEventListener("click", startNewChat);
elements.errorClose.addEventListener("click", hideError);
window.addEventListener("beforeunload", stopGeneration);
window.addEventListener("resize", updateComposerClearance);

if ("ResizeObserver" in window) {
  const composerObserver = new ResizeObserver(updateComposerClearance);
  composerObserver.observe(elements.composerWrap);
}

loadStoredChat();
if (!state.systemPrompt) state.systemPrompt = FALLBACK_SYSTEM_PROMPT;
elements.systemPrompt.value = state.systemPrompt;
updateSystemCount();
renderMessages();
resizeInput();
updateComposerState();
updateComposerClearance();
void refreshStatus();
window.setInterval(() => {
  if (!state.generating) void refreshStatus();
}, 15000);
