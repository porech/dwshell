package auth

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestLoginConfigLiteralConcatenation(t *testing.T) {
	raw := `{"baseUrl":"https://access.dwservice.net/","requestUrl":"https://access.dwservice.net/","cryptAlgorithmAccept":[{"importKey":{"keyData":"synthetic-server-public-key"}}]}`
	encoded := hex.EncodeToString([]byte(raw))
	mid := len(encoded) / 2
	for _, expr := range []string{
		"'" + encoded + "'",
		"'" + encoded[:mid] + "'+'" + encoded[mid:] + "'",
		"\"" + encoded[:mid] + "\" + \n \"" + encoded[mid:] + "\"",
	} {
		cfg, err := parseLoginConfig([]byte("loadConfiguration(dwsInitDecodeHex(" + expr + "))"))
		if err != nil || cfg.BaseURL != "https://access.dwservice.net/" || cfg.ServerKeyB64 != "synthetic-server-public-key" {
			t.Fatal("literal config not decoded correctly")
		}
	}
	for _, expr := range []string{
		"'" + encoded[:mid] + "'+callFunction()+'" + encoded[mid:] + "'",
		"'" + encoded[:mid] + "'+window.value",
		"'" + strings.Repeat("g", 300) + "'",
	} {
		if _, err := parseLoginConfig([]byte("dwsInitDecodeHex(" + expr + ")")); err == nil {
			t.Fatal("nonliteral config accepted")
		}
	}
}
