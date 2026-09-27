package mcpcontract

func Object(properties map[string]any, required ...string) map[string]any {
	result := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}
func String(min, max int) map[string]any {
	return map[string]any{"type": "string", "minLength": min, "maxLength": max}
}
func Error() map[string]any {
	return Object(map[string]any{"code": String(1, 100), "message": String(1, 1000)}, "code", "message")
}
func Source() map[string]any {
	return Object(map[string]any{"title": String(1, 500), "url": map[string]any{"type": "string", "minLength": 1, "maxLength": 2048, "format": "uri"}}, "title", "url")
}
func Document() map[string]any {
	return Object(map[string]any{"title": String(1, 500), "url": map[string]any{"type": "string", "minLength": 1, "maxLength": 2048, "format": "uri"}, "text": String(1, 100000)}, "title", "url", "text")
}
