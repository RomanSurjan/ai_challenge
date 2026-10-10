package generation

// Qwen2.5 uses GPT2 byte-level BPE. Merges only reduce the initial byte
// token count. 256 covers the observed ChatML framing of two messages.
// Conservative fallback, not an exact token count; use loaded tokenizer first.
func InputBound(system, prompt string) int { return len([]byte(system)) + len([]byte(prompt)) + 256 }
