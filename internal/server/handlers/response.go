package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// writeJSON は status とともに v を JSON エンコードしてレスポンスに書き込みます。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// ヘッダーは送信済みなので、ここでステータスは変えられません。
		slog.Error("failed to encode JSON response", "error", err)
	}
}

// writeErrorJSON は status とエラーメッセージを JSON 形式でレスポンスに書き込みます。
func writeErrorJSON(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
